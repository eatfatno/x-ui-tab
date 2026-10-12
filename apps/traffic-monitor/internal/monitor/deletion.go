package monitor

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrDeletionChanged = errors.New("history changed since preview")

type DeletionPreview struct {
	Start             string `json:"start"`
	End               string `json:"end"`
	ClientID          string `json:"client_id"`
	ClientCount       int64  `json:"client_count"`
	VisitCount        int64  `json:"visit_count"`
	SummaryVisitCount int64  `json:"summary_visit_count"`
	TrafficBuckets    int64  `json:"traffic_buckets"`
	Up                int64  `json:"up"`
	Down              int64  `json:"down"`
	Total             int64  `json:"total"`
	Revision          string `json:"-"`
}

func validateDeletion(q Query) error {
	if !q.End.After(q.Start) || q.End.Sub(q.Start) > 35*24*time.Hour || !q.Start.Equal(q.Start.Truncate(time.Minute)) || !q.End.Equal(q.End.Truncate(time.Minute)) {
		return errors.New("删除范围须按分钟对齐且不超过 35 天")
	}
	if q.InboundID != 0 || q.Target != "" {
		return errors.New("删除须按客户端和日期进行，包含该客户端的全部目标和入站")
	}
	return nil
}

func previewDeletion(tx *sql.Tx, q Query) (DeletionPreview, error) {
	x := DeletionPreview{Start: stamp(q.Start), End: stamp(q.End), ClientID: q.ClientID}
	w, a := filter(q, "visits")
	// Stream stable row representations into the digest. Same counts alone do not
	// prove that the user is confirming the same history.
	h := sha256.New()
	clients := map[string]bool{}
	for _, table := range []string{"visits", "visit_counts", "traffic"} {
		cols := "id,time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status"
		if table == "visit_counts" {
			cols = "time,client_id,inbound_id,count"
		}
		if table == "traffic" {
			cols = "time,client_id,inbound_ids,up,down"
		}
		rows, e := tx.Query("SELECT "+cols+" FROM "+table+" WHERE "+w+" ORDER BY "+cols, a...)
		if e != nil {
			return x, e
		}
		fmt.Fprintln(h, table)
		names, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(names))
			ptrs := make([]any, len(names))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if e = rows.Scan(ptrs...); e != nil {
				rows.Close()
				return x, e
			}
			encoded, e := json.Marshal(values)
			if e != nil {
				rows.Close()
				return x, e
			}
			h.Write(encoded)
			h.Write([]byte{0})
			switch table {
			case "visits":
				clients[values[2].(string)] = true
				x.VisitCount++
			case "visit_counts":
				clients[values[1].(string)] = true
				x.SummaryVisitCount += values[3].(int64)
			case "traffic":
				clients[values[1].(string)] = true
				x.TrafficBuckets++
				x.Up += values[3].(int64)
				x.Down += values[4].(int64)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return x, e
		}
	}
	x.ClientCount = int64(len(clients))
	x.Total = x.Up + x.Down
	x.Revision = hex.EncodeToString(h.Sum(nil))
	return x, nil
}

func (s *Store) PreviewDeletion(q Query) (x DeletionPreview, e error) {
	if e = validateDeletion(q); e != nil {
		return
	}
	e = s.tx(func(tx *sql.Tx) error { var err error; x, err = previewDeletion(tx, q); return err })
	return
}

func (s *Store) DeleteHistory(q Query, expected DeletionPreview, now time.Time) (x DeletionPreview, e error) {
	if e = validateDeletion(q); e != nil {
		return
	}
	e = s.tx(func(tx *sql.Tx) error {
		var err error
		x, err = previewDeletion(tx, q)
		if err != nil {
			return err
		}
		if x != expected {
			return ErrDeletionChanged
		}
		w, a := filter(q, "visits")
		for _, table := range []string{"visits", "visit_counts", "traffic"} {
			if _, err = tx.Exec("DELETE FROM "+table+" WHERE "+w, a...); err != nil {
				return err
			}
		}
		// Keep counter baselines and log checkpoints. Tombstones additionally stop
		// late records or copied/rotated source files from resurrecting deletions.
		if _, err = tx.Exec("INSERT OR IGNORE INTO history_deletions(start,end,client_id,created_at) VALUES(?,?,?,?)", q.Start.UnixMilli(), q.End.UnixMilli(), q.ClientID, now.UnixMilli()); err != nil {
			return err
		}
		return quality(tx, q.Start, q.End, q.ClientID, "history_deleted")
	})
	return
}

func deletedVisit(tx *sql.Tx, at time.Time, id string) (bool, error) {
	var found bool
	e := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM history_deletions WHERE start<=? AND end>? AND (client_id='' OR client_id=?))", at.UnixMilli(), at.UnixMilli(), id).Scan(&found)
	return found, e
}

func deletedSample(tx *sql.Tx, prev, now time.Time, id string) (bool, error) {
	var found bool
	e := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM history_deletions WHERE (client_id='' OR client_id=?) AND ((start<? AND end>?) OR (start<=? AND end>?)))", id, now.UnixMilli(), prev.UnixMilli(), now.Truncate(time.Minute).UnixMilli(), now.Truncate(time.Minute).UnixMilli()).Scan(&found)
	return found, e
}
