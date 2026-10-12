package monitor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Opt-in, isolated capacity profile with production-shaped identities and both
// 35-day aggregate tables. No connection to the running panel or its database.
func TestResourceConcurrentPeak(t *testing.T) {
	output := os.Getenv("MONITOR_PEAK_OUTPUT")
	if output == "" {
		t.Skip("set MONITOR_PEAK_OUTPUT to a new project report path")
	}
	dir := strings.TrimSuffix(output, filepath.Ext(output)) + "-data"
	must(t, os.Mkdir(dir, 0700)) // Never overwrite an existing fixture.
	c := DefaultConfig()
	c.DBPath = filepath.Join(dir, "monitor.db")
	now := time.Now().UTC().Truncate(time.Minute)
	start := now.Add(-35 * 24 * time.Hour)
	s, e := OpenStore(c, start)
	must(t, e)
	defer s.Close()
	snaps := make([]Snapshot, 5)
	for i := range snaps {
		snaps[i] = snap(panelClientID("capacity-lab", strconv.Itoa(i+1), start.UnixMilli()), fmt.Sprintf("capacity-device-%d", i+1), 1000, 2000)
	}
	must(t, s.Sample(start, snaps))
	t.Log("seeding 700,000 details and complete 35-day summaries")
	must(t, s.tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`WITH RECURSIVE seq(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM seq WHERE i<699999),
clients_by_number AS (SELECT *,ROW_NUMBER() OVER (ORDER BY id)-1 AS n FROM clients)
INSERT INTO visits(time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status)
SELECT ?+(i*7337)%604740000,c.id,c.email,
CASE i%7 WHEN 0 THEN 'r'||(i%100)||'.googlevideo.com' WHEN 1 THEN 'api.telegram.org' WHEN 2 THEN 'video.twimg.com' WHEN 3 THEN 'ios.chat.openai.com' WHEN 4 THEN 'acs.m.taobao.com' WHEN 5 THEN 'p'||(i%300)||'.example.test' ELSE 'i'||(i%80)||'.douyinvod.com' END,
443,'tcp','capacity-inbound',1,'direct','accepted' FROM seq JOIN clients_by_number c ON c.n=i%5`, now.Add(-7*24*time.Hour).UnixMilli())
		if err != nil {
			return err
		}
		_, err = tx.Exec(`WITH RECURSIVE seq(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM seq WHERE i<50399)
INSERT INTO traffic SELECT ?+i*60000,c.id,'[1]',1048576,7340032 FROM seq CROSS JOIN clients c`, start.UnixMilli())
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO visit_counts SELECT (time/60000)*60000,client_id,inbound_id,COUNT(*) FROM visits GROUP BY 1,2,3`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`WITH RECURSIVE seq(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM seq WHERE i<201599),
clients_by_number AS (SELECT *,ROW_NUMBER() OVER (ORDER BY id)-1 AS n FROM clients)
INSERT INTO visit_counts SELECT ?+(i/5)*60000,c.id,1,13+CASE WHEN i<179200 THEN 1 ELSE 0 END FROM seq JOIN clients_by_number c ON c.n=i%5`, start.UnixMilli())
		if err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE clients SET last_seen=?", now.UnixMilli())
		return err
	}))
	var visits, traffic, counts, total int64
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM visits").Scan(&visits))
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM traffic").Scan(&traffic))
	must(t, s.db.QueryRow("SELECT COUNT(*),SUM(count) FROM visit_counts").Scan(&counts, &total))
	if visits != 700000 || traffic != 252000 || counts < 251990 || counts > 252000 || total != 3500000 {
		t.Fatalf("incomplete fixture: %d %d %d %d", visits, traffic, counts, total)
	}
	stat, e := os.Stat(c.DBPath)
	must(t, e)
	report := map[string]any{"synthetic": true, "clients": 5, "detail_rows": visits, "traffic_rows": traffic, "visit_count_rows": counts, "visits_in_35_days": total, "database_bytes": stat.Size(), "identity_bytes": len(snaps[0].ID), "cpu_affinity_count": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "gomemlimit": os.Getenv("GOMEMLIMIT"), "note": "Store workload on one WSL CPU; excludes panel, HTTP auth, OS and Xray encryption. Peak CPU is sampled over approximately one-second windows, not a VPS guarantee."}
	cpu := func() float64 {
		var r syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &r)
		return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
	}
	rss := func() int64 {
		b, _ := os.ReadFile("/proc/self/status")
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) > 1 && f[0] == "VmRSS:" {
				n, _ := strconv.ParseInt(f[1], 10, 64)
				return n * 1024
			}
		}
		return 0
	}
	type timing struct {
		Name   string  `json:"name"`
		Millis float64 `json:"ms"`
	}
	stages := []map[string]any{}
	for _, days := range []int{1, 7} {
		label := fmt.Sprintf("%dd_dashboard_and_2000_visits_per_10s", days)
		t.Log(label)
		started := time.Now()
		beforeCPU := cpu()
		sampleCPU := beforeCPU
		sampleAt := started
		peakCPU, peakRSS := float64(0), rss()
		var timings []timing
		var mu sync.Mutex
		var wg sync.WaitGroup
		errors := make(chan error, 64)
		record := func(name string, fn func() error) {
			a := time.Now()
			err := fn()
			mu.Lock()
			timings = append(timings, timing{name, float64(time.Since(a).Microseconds()) / 1000})
			mu.Unlock()
			if err != nil {
				errors <- err
			}
		}
		// A dashboard refresh sends its cards/trend/table/client requests together.
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := allQuery(now.Add(-time.Duration(days)*24*time.Hour), now)
			for cycle := 0; cycle < 2; cycle++ {
				var queries sync.WaitGroup
				for name, fn := range map[string]func() error{
					"summary": func() error { _, e := s.Summary(q, now); return e },
					"traffic": func() error { _, e := s.Traffic(q, now); return e },
					"targets": func() error { _, e := s.Targets(q, now); return e },
					"clients": func() error { _, e := s.Clients(now); return e },
				} {
					queries.Add(1)
					go func(name string, fn func() error) { defer queries.Done(); record(name, fn) }(name, fn)
				}
				queries.Wait()
				if cycle == 0 {
					time.Sleep(2 * time.Second)
				}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := 0; batch < 4; batch++ {
				at := now.Add(time.Duration(len(stages)*60+batch*10+10) * time.Second)
				for i := range snaps {
					snaps[i].Up += 1 << 20
					snaps[i].Down += 7 << 20
				}
				record("sample_5_clients", func() error { return s.Sample(at, snaps) })
				v := make([]Visit, 2000)
				for i := range v {
					v[i] = Visit{At: at, Email: snaps[i%5].Email, Target: "api.telegram.org", Port: 443, Protocol: "tcp", Inbound: "capacity-inbound", Status: "accepted"}
				}
				record("ingest_2000", func() error { return s.Ingest(label, Checkpoint{Offset: int64((batch + 1) * 400000)}, v, at, false) })
				if batch < 3 {
					time.Sleep(10 * time.Second)
				}
			}
		}()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		ticker := time.NewTicker(time.Second)
		finished := false
		for !finished {
			select {
			case <-ticker.C:
			case <-done:
				finished = true
			}
			a := time.Now()
			b := cpu()
			pct := (b - sampleCPU) / a.Sub(sampleAt).Seconds() * 100
			if a.Sub(sampleAt) >= 500*time.Millisecond && pct > peakCPU {
				peakCPU = pct
			}
			sampleAt, sampleCPU = a, b
			if r := rss(); r > peakRSS {
				peakRSS = r
			}
		}
		ticker.Stop()
		close(errors)
		for err := range errors {
			must(t, err)
		}
		sort.Slice(timings, func(i, j int) bool { return timings[i].Name < timings[j].Name })
		elapsed := time.Since(started).Seconds()
		stages = append(stages, map[string]any{"name": label, "wall_seconds": elapsed, "cpu_percent_one_core": (cpu() - beforeCPU) / elapsed * 100, "peak_cpu_percent_one_core": peakCPU, "peak_rss_bytes": peakRSS, "timings": timings})
	}
	q := allQuery(start, now)
	a := time.Now()
	sum, e := s.Summary(q, now)
	must(t, e)
	if sum.VisitCount != 3500000 {
		t.Fatalf("summary count %d", sum.VisitCount)
	}
	report["summary_35d_ms"] = float64(time.Since(a).Microseconds()) / 1000
	a = time.Now()
	_, e = s.Traffic(q, now)
	must(t, e)
	report["traffic_35d_ms"] = float64(time.Since(a).Microseconds()) / 1000
	report["stages"] = stages
	var integrity string
	must(t, s.db.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	report["integrity"] = integrity
	data, e := json.MarshalIndent(report, "", "  ")
	must(t, e)
	must(t, os.WriteFile(output, append(data, '\n'), 0600))
	t.Log(output)
}
