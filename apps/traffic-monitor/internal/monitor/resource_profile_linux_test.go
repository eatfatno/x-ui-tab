package monitor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Explicit opt-in profile: real Go store/driver on the same WSL mounted disk,
// with synthetic connection records, no changes to the active monitor database.
func TestResourceProfile(t *testing.T) {
	output := os.Getenv("MONITOR_PROFILE_OUTPUT")
	if output == "" {
		t.Skip("set MONITOR_PROFILE_OUTPUT to run capacity profile")
	}
	dir := strings.TrimSuffix(output, filepath.Ext(output)) + "-data"
	must(t, os.MkdirAll(dir, 0700))
	now := time.Now().UTC().Truncate(time.Minute)
	start := now.Add(-35 * 24 * time.Hour)
	c := DefaultConfig()
	c.DBPath = filepath.Join(dir, "monitor.db")
	s, e := OpenStore(c, start)
	must(t, e)
	defer s.Close()
	var existing int
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM visits").Scan(&existing))
	if existing != 0 {
		t.Fatal("profile requires a fresh fixture")
	}
	rows := []Snapshot{}
	for i := 0; i < 5; i++ {
		rows = append(rows, snap(fmt.Sprint(i), fmt.Sprintf("client-%d", i), 1000, 2000))
	}
	must(t, s.Sample(start, rows))
	type measure struct {
		Name      string  `json:"name"`
		Millis    float64 `json:"wall_ms"`
		CPUMillis float64 `json:"cpu_ms"`
	}
	measurements := []measure{}
	cpu := func() float64 {
		var u syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &u)
		return float64(u.Utime.Sec+u.Stime.Sec)*1000 + float64(u.Utime.Usec+u.Stime.Usec)/1000
	}
	run := func(name string, fn func() error) {
		t.Helper()
		a := time.Now()
		b := cpu()
		must(t, fn())
		measurements = append(measurements, measure{name, float64(time.Since(a).Microseconds()) / 1000, cpu() - b})
	}
	for _, n := range []int{70000, 700000} {
		run(fmt.Sprintf("seed_%d", n), func() error {
			return s.tx(func(tx *sql.Tx) error {
				_, e := tx.Exec(`WITH RECURSIVE seq(i) AS (SELECT ? UNION ALL SELECT i+1 FROM seq WHERE i<?)
INSERT INTO visits(time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status)
SELECT ?+(i*7337)%604740000,CAST(i%5 AS TEXT),'client-'||(i%5),
CASE i%7 WHEN 0 THEN 'r'||(i%100)||'.googlevideo.com' WHEN 1 THEN 'api.telegram.org' WHEN 2 THEN 'video.twimg.com' WHEN 3 THEN 'ios.chat.openai.com' WHEN 4 THEN 'acs.m.taobao.com' WHEN 5 THEN 'p'||(i%300)||'.example.test' ELSE 'i'||(i%80)||'.douyinvod.com' END,
CASE i%2 WHEN 0 THEN 443 ELSE 80 END,'tcp','inbound-1',1,'direct','accepted' FROM seq`, existing+1, n, now.Add(-7*24*time.Hour).UnixMilli())
				return e
			})
		})
		existing = n
		q := allQuery(now.Add(-7*24*time.Hour), now)
		for repeat := 0; repeat < 3; repeat++ {
			run(fmt.Sprintf("targets_%d_%d", n, repeat), func() error { _, e := s.Targets(q, now); return e })
			run(fmt.Sprintf("visits_%d_%d", n, repeat), func() error { _, e := s.Visits(q, now); return e })
		}
		q.ClientID = "0"
		q.Target = "YouTube"
		run(fmt.Sprintf("filtered_%d", n), func() error { _, e := s.Targets(q, now); return e })
	}
	run("traffic_35_days_seed", func() error {
		return s.tx(func(tx *sql.Tx) error {
			_, e := tx.Exec(`WITH RECURSIVE seq(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM seq WHERE i<251999) INSERT INTO traffic SELECT ?+(i/5)*60000,CAST(i%5 AS TEXT),'[1]',1024,8192 FROM seq`, start.UnixMilli())
			return e
		})
	})
	q := allQuery(start, now)
	run("summary_35_days_5_clients", func() error { _, e := s.Summary(q, now); return e })
	run("trend_35_days_5_clients", func() error { _, e := s.Traffic(q, now); return e })
	visits := make([]Visit, 2000)
	for i := range visits {
		visits[i] = Visit{At: now.Add(time.Duration(i%10) * time.Second), Email: fmt.Sprintf("client-%d", i%5), Target: "api.telegram.org", Port: 443, Protocol: "tcp", Inbound: "inbound-1", Status: "accepted"}
	}
	run("ingest_2000_records", func() error {
		return s.Ingest("profile-log", Checkpoint{Offset: 400000}, visits, now.Add(10*time.Second), false)
	})
	q = allQuery(now.Add(-time.Hour), now)
	q.ClientID = "0"
	var preview DeletionPreview
	run("delete_preview_one_client_one_hour", func() error { var e error; preview, e = s.PreviewDeletion(q); return e })
	run("delete_commit_one_client_one_hour", func() error { _, e := s.DeleteHistory(q, preview, now); return e })
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	stat, e := os.Stat(c.DBPath)
	must(t, e)
	proc, _ := os.ReadFile("/proc/self/status")
	rss, peak := int64(0), int64(0)
	for _, line := range strings.Split(string(proc), "\n") {
		parts := strings.Fields(line)
		if len(parts) > 1 && (parts[0] == "VmRSS:" || parts[0] == "VmHWM:") {
			v, _ := strconv.ParseInt(parts[1], 10, 64)
			if parts[0] == "VmRSS:" {
				rss = v * 1024
			} else {
				peak = v * 1024
			}
		}
	}
	report := map[string]any{"synthetic": true, "cpu_affinity_count": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "gomemlimit": os.Getenv("GOMEMLIMIT"), "records": 700000, "clients": 5, "traffic_minute_rows": 252000, "database_bytes": stat.Size(), "rss_bytes": rss, "peak_rss_bytes": peak, "go_heap_bytes": m.HeapAlloc, "measurements": measurements, "note": "Measured on WSL NTFS mount, synthetic records; CPU affinity limits concurrency, not VPS clock speed. No Xray data-plane or TLS encryption benchmark."}
	data, e := json.MarshalIndent(report, "", "  ")
	must(t, e)
	must(t, os.WriteFile(output, append(data, '\n'), 0600))
	t.Logf("report %s; DB %.1f MiB; peak RSS %.1f MiB", output, float64(stat.Size())/(1<<20), float64(peak)/(1<<20))
}

// Reuse the synthetic capacity fixture for common short queries, without
// reseeding it or reading the active service's database.
func TestResourceRecentWindows(t *testing.T) {
	base := os.Getenv("MONITOR_PROFILE_FIXTURE")
	if base == "" {
		t.Skip("set MONITOR_PROFILE_FIXTURE to an existing synthetic profile report")
	}
	blob, e := os.ReadFile(base)
	must(t, e)
	var profile struct {
		Synthetic bool `json:"synthetic"`
	}
	must(t, json.Unmarshal(blob, &profile))
	if !profile.Synthetic {
		t.Fatal("only synthetic profile fixtures are allowed")
	}
	c := DefaultConfig()
	c.DBPath = strings.TrimSuffix(base, filepath.Ext(base)) + "-data/monitor.db"
	s, e := OpenStore(c, time.Now())
	must(t, e)
	defer s.Close()
	var last int64
	must(t, s.db.QueryRow("SELECT MAX(time) FROM traffic").Scan(&last))
	now := time.UnixMilli(last).Add(time.Minute)
	results := map[string]any{"synthetic": true, "source_report": filepath.Base(base), "cpu_affinity_count": runtime.NumCPU(), "gomemlimit": os.Getenv("GOMEMLIMIT")}
	for _, hours := range []int{1, 24} {
		values := []float64{}
		for i := 0; i < 3; i++ {
			before := time.Now()
			_, e = s.Targets(allQuery(now.Add(-time.Duration(hours)*time.Hour), now), now)
			must(t, e)
			values = append(values, float64(time.Since(before).Microseconds())/1000)
		}
		results[fmt.Sprintf("targets_%dh_ms", hours)] = values
	}
	data, e := json.MarshalIndent(results, "", "  ")
	must(t, e)
	output := strings.TrimSuffix(base, filepath.Ext(base)) + "-recent.json"
	must(t, os.WriteFile(output, append(data, '\n'), 0600))
	t.Log(output)
}
