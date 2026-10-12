package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// A single connection serializes SQLite access on WSL's mounted NTFS. Mutations
// additionally hold writeMu so each multi-statement operation is one transaction.
type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
	cfg     Config
	loc     *time.Location
	started time.Time
}

const schema = `
PRAGMA journal_mode=DELETE;
PRAGMA synchronous=FULL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS clients(id TEXT PRIMARY KEY,email TEXT NOT NULL,inbound_ids TEXT NOT NULL,deleted INTEGER NOT NULL DEFAULT 0,up INTEGER NOT NULL,down INTEGER NOT NULL,last_seen INTEGER NOT NULL,has_traffic INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS aliases(client_id TEXT NOT NULL,email TEXT NOT NULL,valid_from INTEGER NOT NULL,valid_to INTEGER,PRIMARY KEY(client_id,email,valid_from));
CREATE INDEX IF NOT EXISTS alias_email_time ON aliases(email,valid_from,valid_to);
CREATE TABLE IF NOT EXISTS inbound_aliases(tag TEXT NOT NULL,inbound_id INTEGER NOT NULL,valid_from INTEGER NOT NULL,valid_to INTEGER,PRIMARY KEY(tag,inbound_id,valid_from));
CREATE TABLE IF NOT EXISTS traffic(time INTEGER NOT NULL,client_id TEXT NOT NULL,inbound_ids TEXT NOT NULL,up INTEGER NOT NULL,down INTEGER NOT NULL,PRIMARY KEY(time,client_id,inbound_ids));
CREATE INDEX IF NOT EXISTS traffic_client_time ON traffic(client_id,time);
CREATE TABLE IF NOT EXISTS visits(id INTEGER PRIMARY KEY,time INTEGER NOT NULL,client_id TEXT NOT NULL,email TEXT NOT NULL,target TEXT NOT NULL,port INTEGER NOT NULL,protocol TEXT NOT NULL,inbound TEXT NOT NULL,inbound_id INTEGER NOT NULL,outbound TEXT NOT NULL,status TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS visit_time ON visits(time);
CREATE INDEX IF NOT EXISTS visit_client_time ON visits(client_id,time);
CREATE INDEX IF NOT EXISTS visit_inbound_time ON visits(inbound_id,time);
CREATE INDEX IF NOT EXISTS visit_target_cover ON visits(time,client_id,email,target,port,protocol,inbound,inbound_id);
CREATE TABLE IF NOT EXISTS visit_counts(time INTEGER NOT NULL,client_id TEXT NOT NULL,inbound_id INTEGER NOT NULL,count INTEGER NOT NULL,PRIMARY KEY(time,client_id,inbound_id));
CREATE INDEX IF NOT EXISTS count_client_time ON visit_counts(client_id,time);
CREATE TABLE IF NOT EXISTS quality(start INTEGER NOT NULL,end INTEGER NOT NULL,client_id TEXT NOT NULL,code TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS quality_time ON quality(start,end);
CREATE TABLE IF NOT EXISTS checkpoints(file_id TEXT PRIMARY KEY,offset INTEGER NOT NULL,anchor TEXT NOT NULL,first_seen INTEGER NOT NULL,updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS history_deletions(start INTEGER NOT NULL,end INTEGER NOT NULL,client_id TEXT NOT NULL,created_at INTEGER NOT NULL,PRIMARY KEY(start,end,client_id));
CREATE INDEX IF NOT EXISTS history_deletion_client_time ON history_deletions(client_id,start,end);
`

func OpenStore(c Config, now time.Time) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(c.DBPath), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", c.DBPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec("INSERT OR IGNORE INTO meta(key,value) VALUES('started_at',?)", stamp(now)); err != nil {
		db.Close()
		return nil, err
	}
	var st string
	if err = db.QueryRow("SELECT value FROM meta WHERE key='started_at'").Scan(&st); err != nil {
		db.Close()
		return nil, err
	}
	started, err := time.Parse(time.RFC3339Nano, st)
	if err != nil {
		db.Close()
		return nil, err
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, cfg: c, loc: loc, started: started}
	if err = s.migratePanelIdentities(); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.removeInternalVisits(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) tx(fn func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func setMeta(tx *sql.Tx, key, value string) error {
	_, e := tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return e
}
func (s *Store) Meta(key string) string {
	var v string
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&v)
	return v
}
func quality(tx *sql.Tx, start, end time.Time, id, code string) error {
	_, e := tx.Exec("INSERT INTO quality(start,end,client_id,code) VALUES(?,?,?,?)", start.UnixMilli(), end.UnixMilli(), id, code)
	return e
}

func (s *Store) Sample(now time.Time, snaps []Snapshot) error {
	return s.tx(func(tx *sql.Tx) error {
		seen := map[string]bool{}
		for _, p := range snaps {
			if p.ID == "" || p.Email == "" || p.Up < 0 || p.Down < 0 {
				return fmt.Errorf("invalid client snapshot")
			}
			if seen[p.ID] {
				return fmt.Errorf("duplicate client identity")
			}
			seen[p.ID] = true
			if p.InboundIDs == nil {
				p.InboundIDs = []int{}
			}
			ib, _ := json.Marshal(p.InboundIDs)
			var oldEmail, oldIB string
			var up, down, last int64
			var deleted, has int
			err := tx.QueryRow("SELECT email,inbound_ids,up,down,last_seen,deleted,has_traffic FROM clients WHERE id=?", p.ID).Scan(&oldEmail, &oldIB, &up, &down, &last, &deleted, &has)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			newClient := err == sql.ErrNoRows
			if !newClient && now.UnixMilli() <= last {
				return fmt.Errorf("non-monotonic sample")
			}
			if !newClient && has == 1 && p.HasTraffic && deleted == 0 {
				prev := time.UnixMilli(last)
				code := ""
				du, dd := p.Up-up, p.Down-down
				if du < 0 || dd < 0 {
					code = "counter_reset"
				} else if now.Sub(prev) > s.cfg.Interval()*3 {
					code = "collection_gap"
				}
				if code != "" {
					if e := quality(tx, prev, now, p.ID, code); e != nil {
						return e
					}
				}
				// Across a local day boundary, an outage cannot be allocated honestly.
				if code != "counter_reset" && !(code == "collection_gap" && prev.In(s.loc).Format("2006-01-02") != now.In(s.loc).Format("2006-01-02")) {
					blocked, e := deletedSample(tx, prev, now, p.ID)
					if e != nil {
						return e
					}
					if !blocked && (du != 0 || dd != 0) {
						_, e = tx.Exec("INSERT INTO traffic(time,client_id,inbound_ids,up,down) VALUES(?,?,?,?,?) ON CONFLICT(time,client_id,inbound_ids) DO UPDATE SET up=up+excluded.up,down=down+excluded.down", now.Truncate(time.Minute).UnixMilli(), p.ID, string(ib), du, dd)
						if e != nil {
							return e
						}
					}
				}
			} else if !newClient && (!p.HasTraffic || deleted == 1) {
				if e := quality(tx, time.UnixMilli(last), now, p.ID, "collection_gap"); e != nil {
					return e
				}
			}
			if newClient || oldEmail != p.Email || deleted == 1 {
				if _, e := tx.Exec("UPDATE aliases SET valid_to=? WHERE client_id=? AND valid_to IS NULL", now.UnixMilli(), p.ID); e != nil {
					return e
				}
				_, e := tx.Exec("INSERT INTO aliases(client_id,email,valid_from) VALUES(?,?,?)", p.ID, p.Email, now.UnixMilli())
				if e != nil {
					return e
				}
			}
			_, e := tx.Exec("INSERT INTO clients(id,email,inbound_ids,deleted,up,down,last_seen,has_traffic) VALUES(?,?,?,0,?,?,?,?) ON CONFLICT(id) DO UPDATE SET email=excluded.email,inbound_ids=excluded.inbound_ids,deleted=0,up=excluded.up,down=excluded.down,last_seen=excluded.last_seen,has_traffic=excluded.has_traffic", p.ID, p.Email, string(ib), p.Up, p.Down, now.UnixMilli(), p.HasTraffic)
			if e != nil {
				return e
			}
		}
		rows, e := tx.Query("SELECT id FROM clients WHERE deleted=0")
		if e != nil {
			return e
		}
		removed := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			if !seen[id] {
				removed = append(removed, id)
			}
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return e
		}
		rows.Close()
		for _, id := range removed {
			if _, e = tx.Exec("UPDATE clients SET deleted=1 WHERE id=?", id); e != nil {
				return e
			}
			if _, e = tx.Exec("UPDATE aliases SET valid_to=? WHERE client_id=? AND valid_to IS NULL", now.UnixMilli(), id); e != nil {
				return e
			}
		}
		return setMeta(tx, "last_sample_at", stamp(now))
	})
}

type Checkpoint struct {
	Offset    int64
	Anchor    string
	FirstSeen int64
}

func (s *Store) SyncInbounds(now time.Time, tags map[string]int) error {
	return s.tx(func(tx *sql.Tx) error {
		rows, e := tx.Query("SELECT tag FROM inbound_aliases WHERE valid_to IS NULL")
		if e != nil {
			return e
		}
		removed := []string{}
		for rows.Next() {
			var tag string
			if e = rows.Scan(&tag); e != nil {
				rows.Close()
				return e
			}
			if _, ok := tags[tag]; !ok {
				removed = append(removed, tag)
			}
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return e
		}
		rows.Close()
		for _, tag := range removed {
			if _, e = tx.Exec("UPDATE inbound_aliases SET valid_to=? WHERE tag=? AND valid_to IS NULL", now.UnixMilli(), tag); e != nil {
				return e
			}
		}
		for tag, id := range tags {
			var prev int
			e := tx.QueryRow("SELECT inbound_id FROM inbound_aliases WHERE tag=? AND valid_to IS NULL", tag).Scan(&prev)
			if e == nil && prev == id {
				continue
			}
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			if _, e = tx.Exec("UPDATE inbound_aliases SET valid_to=? WHERE tag=? AND valid_to IS NULL", now.UnixMilli(), tag); e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO inbound_aliases(tag,inbound_id,valid_from) VALUES(?,?,?)", tag, id, now.UnixMilli()); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Checkpoint(id string) (Checkpoint, error) {
	var c Checkpoint
	err := s.db.QueryRow("SELECT offset,anchor,first_seen FROM checkpoints WHERE file_id=?", id).Scan(&c.Offset, &c.Anchor, &c.FirstSeen)
	if err == sql.ErrNoRows {
		return c, nil
	}
	return c, err
}

func (s *Store) Ingest(fileID string, cp Checkpoint, visits []Visit, now time.Time, malformed bool) error {
	return s.tx(func(tx *sql.Tx) error {
		if malformed {
			if e := quality(tx, now, now, "", "log_parse_error"); e != nil {
				return e
			}
		}
		for _, v := range visits {
			if internalVisit(v) {
				continue
			}
			if v.At.Before(s.started) || v.At.Before(now.AddDate(0, 0, -s.cfg.DetailDays)) || v.At.After(now.Add(5*time.Minute)) {
				continue
			}
			var mapped int
			e := tx.QueryRow("SELECT inbound_id FROM inbound_aliases WHERE tag=? AND valid_from<=? AND (valid_to IS NULL OR valid_to>?) ORDER BY valid_from DESC LIMIT 1", v.Inbound, v.At.UnixMilli(), v.At.UnixMilli()).Scan(&mapped)
			if e == nil {
				v.InboundID = mapped
			} else if e != sql.ErrNoRows {
				return e
			}
			rows, e := tx.Query("SELECT DISTINCT client_id FROM aliases WHERE email=? AND valid_from<=? AND (valid_to IS NULL OR valid_to>?)", v.Email, v.At.UnixMilli(), v.At.UnixMilli())
			if e != nil {
				return e
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return e
				}
				ids = append(ids, id)
			}
			if e = rows.Err(); e != nil {
				rows.Close()
				return e
			}
			rows.Close()
			if len(ids) == 1 {
				v.ClientID = ids[0]
			} else {
				if e = quality(tx, v.At, v.At, "", "unmatched_identity"); e != nil {
					return e
				}
			}
			blocked, e := deletedVisit(tx, v.At, v.ClientID)
			if e != nil {
				return e
			}
			if blocked {
				continue
			}
			_, e = tx.Exec("INSERT INTO visits(time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status) VALUES(?,?,?,?,?,?,?,?,?,?)", v.At.UnixMilli(), v.ClientID, v.Email, v.Target, v.Port, v.Protocol, v.Inbound, v.InboundID, v.Outbound, v.Status)
			if e != nil {
				return e
			}
			// Minute count buckets preserve arbitrary date boundaries; the UI can aggregate days.
			_, e = tx.Exec("INSERT INTO visit_counts(time,client_id,inbound_id,count) VALUES(?,?,?,1) ON CONFLICT(time,client_id,inbound_id) DO UPDATE SET count=count+1", v.At.Truncate(time.Minute).UnixMilli(), v.ClientID, v.InboundID)
			if e != nil {
				return e
			}
			if e = setMeta(tx, "last_log_at", stamp(v.At)); e != nil {
				return e
			}
		}
		if cp.FirstSeen == 0 {
			cp.FirstSeen = now.UnixMilli()
		}
		_, e := tx.Exec("INSERT INTO checkpoints(file_id,offset,anchor,first_seen,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(file_id) DO UPDATE SET offset=excluded.offset,anchor=excluded.anchor,updated_at=excluded.updated_at", fileID, cp.Offset, cp.Anchor, cp.FirstSeen, now.UnixMilli())
		return e
	})
}

// Older development builds ingested Xray's own localhost API requests. Remove
// exactly those rows and their count contributions without resetting user data.
func (s *Store) removeInternalVisits() error {
	return s.tx(func(tx *sql.Tx) error {
		const predicate = "email='' AND inbound IN ('api','metrics') AND outbound=inbound AND target IN ('127.0.0.1','::1','localhost')"
		rows, e := tx.Query("SELECT (time/60000)*60000,client_id,inbound_id,COUNT(*) FROM visits WHERE " + predicate + " GROUP BY (time/60000)*60000,client_id,inbound_id")
		if e != nil {
			return e
		}
		type count struct {
			at      int64
			id      string
			inbound int
			n       int
		}
		counts := []count{}
		for rows.Next() {
			var c count
			if e = rows.Scan(&c.at, &c.id, &c.inbound, &c.n); e != nil {
				rows.Close()
				return e
			}
			counts = append(counts, c)
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return e
		}
		rows.Close()
		for _, c := range counts {
			if _, e = tx.Exec("UPDATE visit_counts SET count=MAX(0,count-?) WHERE time=? AND client_id=? AND inbound_id=?", c.n, c.at, c.id, c.inbound); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("DELETE FROM quality WHERE code='unmatched_identity' AND client_id='' AND start=end AND start IN (SELECT time FROM visits WHERE " + predicate + ") AND NOT EXISTS(SELECT 1 FROM visits WHERE visits.time=quality.start AND visits.client_id='' AND NOT (" + predicate + "))"); e != nil {
			return e
		}
		if _, e = tx.Exec("DELETE FROM visits WHERE " + predicate); e != nil {
			return e
		}
		_, e = tx.Exec("DELETE FROM visit_counts WHERE count=0")
		return e
	})
}

func (s *Store) Prune(now time.Time) error {
	return s.tx(func(tx *sql.Tx) error {
		detail := now.AddDate(0, 0, -s.cfg.DetailDays).UnixMilli()
		summary := now.AddDate(0, 0, -s.cfg.SummaryDays).UnixMilli()
		for _, q := range []struct {
			sql string
			cut int64
		}{{"DELETE FROM visits WHERE time<?", detail}, {"DELETE FROM traffic WHERE time<?", summary}, {"DELETE FROM visit_counts WHERE time<?", summary}, {"DELETE FROM quality WHERE end<?", summary}, {"DELETE FROM history_deletions WHERE end<?", summary}, {"DELETE FROM checkpoints WHERE updated_at<?", detail}, {"DELETE FROM aliases WHERE valid_to IS NOT NULL AND valid_to<?", summary}, {"DELETE FROM inbound_aliases WHERE valid_to IS NOT NULL AND valid_to<?", summary}, {"DELETE FROM clients WHERE deleted=1 AND last_seen<?", summary}} {
			if _, e := tx.Exec(q.sql, q.cut); e != nil {
				return e
			}
		}
		return nil
	})
}

func (s *Store) Clients(now time.Time) ([]Client, error) {
	// Keep identity/alias rows for delayed logs and deletion replay protection,
	// but offer a deleted identity only while it has queryable history.
	detail := now.AddDate(0, 0, -s.cfg.DetailDays).UnixMilli()
	summary := now.AddDate(0, 0, -s.cfg.SummaryDays).UnixMilli()
	rows, e := s.db.Query(`SELECT id,email,inbound_ids,deleted,up,down,last_seen FROM clients c
		WHERE deleted=0
		OR EXISTS(SELECT 1 FROM visits WHERE client_id=c.id AND time>=?)
		OR EXISTS(SELECT 1 FROM traffic WHERE client_id=c.id AND time>=? AND (up>0 OR down>0))
		OR EXISTS(SELECT 1 FROM visit_counts WHERE client_id=c.id AND time>=? AND count>0)
		ORDER BY deleted,email,last_seen DESC,id`, detail, summary, summary)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Client{}
	for rows.Next() {
		var c Client
		var ib string
		var last int64
		if e = rows.Scan(&c.ID, &c.Email, &ib, &c.Deleted, &c.Up, &c.Down, &last); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(ib), &c.InboundIDs); e != nil {
			return nil, e
		}
		c.LastSeen = stamp(time.UnixMilli(last))
		out = append(out, c)
	}
	return out, rows.Err()
}

func filter(q Query, table string) (string, []any) {
	w := "time>=? AND time<?"
	a := []any{q.Start.UnixMilli(), q.End.UnixMilli()}
	if q.ClientID != "" {
		w += " AND client_id=?"
		a = append(a, q.ClientID)
	}
	if q.InboundID > 0 {
		if table == "traffic" {
			w += " AND EXISTS(SELECT 1 FROM json_each(inbound_ids) WHERE value=?)"
		} else {
			w += " AND inbound_id=?"
		}
		a = append(a, q.InboundID)
	}
	return w, a
}
func (s *Store) clamp(q Query, days int, now time.Time) Query {
	cut := now.AddDate(0, 0, -days)
	if q.Start.Before(cut) {
		q.Start = cut
	}
	if q.Start.Before(s.started) {
		q.Start = s.started
	}
	return q
}
func (s *Store) clampAggregate(q Query, now time.Time) Query {
	cut := now.AddDate(0, 0, -s.cfg.SummaryDays)
	if q.Start.Before(cut) {
		q.Start = cut
	}
	if q.Start.Before(s.started.Truncate(time.Minute)) {
		q.Start = s.started.Truncate(time.Minute)
	}
	return q
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (s *Store) Summary(q Query, now time.Time) (Summary, error) {
	x := Summary{Quality: []string{"sampling"}, Timezone: s.cfg.Timezone, MonitorStartedAt: stamp(s.started), DetailAvailableFrom: stamp(maxTime(s.started, now.AddDate(0, 0, -s.cfg.DetailDays))), SummaryAvailableFrom: stamp(maxTime(s.started, now.AddDate(0, 0, -s.cfg.SummaryDays)))}
	if q.Start.Before(s.started) {
		x.Quality = append(x.Quality, "before_monitoring")
	}
	if q.Start.Before(now.AddDate(0, 0, -s.cfg.DetailDays)) {
		x.Quality = append(x.Quality, "detail_expired")
	}
	if q.Start.Before(now.AddDate(0, 0, -s.cfg.SummaryDays)) {
		x.Quality = append(x.Quality, "summary_expired")
	}
	query := s.clampAggregate(q, now)
	w, a := filter(query, "traffic")
	if e := s.db.QueryRow("SELECT COALESCE(SUM(up),0),COALESCE(SUM(down),0) FROM traffic WHERE "+w, a...).Scan(&x.Up, &x.Down); e != nil {
		return x, e
	}
	x.Total = x.Up + x.Down
	latest := Query{Start: now.AddDate(0, 0, -s.cfg.DetailDays), End: now.Add(time.Second), ClientID: q.ClientID, InboundID: q.InboundID}
	lw, la := filter(latest, "visits")
	var lastVisit sql.NullInt64
	if e := s.db.QueryRow("SELECT MAX(time) FROM visits WHERE "+lw, la...).Scan(&lastVisit); e != nil {
		return x, e
	}
	if lastVisit.Valid {
		x.LastVisitAt = stamp(time.UnixMilli(lastVisit.Int64))
	}
	w, a = filter(query, "visit_counts")
	if e := s.db.QueryRow("SELECT COALESCE(SUM(count),0) FROM visit_counts WHERE "+w, a...).Scan(&x.VisitCount); e != nil {
		return x, e
	}
	w = "deleted=0"
	a = nil
	if q.ClientID != "" {
		w += " AND id=?"
		a = append(a, q.ClientID)
	}
	if q.InboundID > 0 {
		w += " AND EXISTS(SELECT 1 FROM json_each(inbound_ids) WHERE value=?)"
		a = append(a, q.InboundID)
	}
	if e := s.db.QueryRow("SELECT COALESCE(SUM(up),0),COALESCE(SUM(down),0) FROM clients WHERE "+w, a...).Scan(&x.PanelUp, &x.PanelDown); e != nil {
		return x, e
	}
	w = "end>=? AND start<?"
	a = []any{query.Start.UnixMilli(), query.End.UnixMilli()}
	if q.ClientID != "" {
		w += " AND (client_id=? OR client_id='')"
		a = append(a, q.ClientID)
	}
	rows, e := s.db.Query("SELECT DISTINCT code FROM quality WHERE "+w, a...)
	if e != nil {
		return x, e
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		if e = rows.Scan(&code); e != nil {
			return x, e
		}
		x.Quality = append(x.Quality, code)
	}
	if q.InboundID > 0 {
		x.Quality = append(x.Quality, "inbound_client_totals")
	}
	return x, rows.Err()
}

func (s *Store) Traffic(q Query, now time.Time) ([]TrafficPoint, error) {
	q = s.clampAggregate(q, now)
	w, a := filter(q, "traffic")
	bucket := int64(60000)
	if q.End.Sub(q.Start) > 48*time.Hour {
		bucket = 3600000
	}
	if q.End.Sub(q.Start) > 14*24*time.Hour {
		bucket = 6 * 3600000
	}
	rows, e := s.db.Query(fmt.Sprintf("SELECT (time/%d)*%d AS bucket,SUM(up),SUM(down) FROM traffic WHERE %s GROUP BY bucket ORDER BY bucket", bucket, bucket, w), a...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []TrafficPoint{}
	for rows.Next() {
		var p TrafficPoint
		var t int64
		if e = rows.Scan(&t, &p.Up, &p.Down); e != nil {
			return nil, e
		}
		p.Time = stamp(time.UnixMilli(t))
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Visits(q Query, now time.Time) (VisitPage, error) {
	x := VisitPage{Items: []Visit{}, Page: q.Page, PageSize: q.PageSize, Expired: q.Start.Before(now.AddDate(0, 0, -s.cfg.DetailDays))}
	q = s.clamp(q, s.cfg.DetailDays, now)
	w, a := destinationFilter(q)
	if e := s.db.QueryRow("SELECT COUNT(*) FROM visits WHERE "+w, a...).Scan(&x.Total); e != nil {
		return x, e
	}
	a = append(a, q.PageSize, (q.Page-1)*q.PageSize)
	rows, e := s.db.Query("SELECT id,time,client_id,email,target,port,protocol,inbound,outbound,status FROM visits WHERE "+w+" ORDER BY time DESC,id DESC LIMIT ? OFFSET ?", a...)
	if e != nil {
		return x, e
	}
	defer rows.Close()
	for rows.Next() {
		var v Visit
		var t int64
		if e = rows.Scan(&v.ID, &t, &v.ClientID, &v.Email, &v.Target, &v.Port, &v.Protocol, &v.Inbound, &v.Outbound, &v.Status); e != nil {
			return x, e
		}
		v.Time = stamp(time.UnixMilli(t))
		v.Destination = IdentifyDestination(v.Target)
		x.Items = append(x.Items, v)
	}
	return x, rows.Err()
}

func (s *Store) RecordIssue(now time.Time, code string) error {
	return s.tx(func(tx *sql.Tx) error { return quality(tx, now, now, "", code) })
}
func compatible(version string) bool {
	return strings.HasPrefix(strings.TrimPrefix(version, "v"), "3.9.")
}
