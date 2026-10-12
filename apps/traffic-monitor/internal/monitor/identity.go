package monitor

import (
	"database/sql"
	"strconv"
	"strings"
)

// 3x-ui 3.9 creates records with millisecond timestamps, then some client
// updates serialize the same creation time at second precision. Preserve the
// official numeric ID and creation second so edits do not invent new clients.
func panelClientID(instance, id string, createdAt int64) string {
	key := instance + ":" + id
	if createdAt != 0 {
		key += ":" + strconv.FormatInt(createdAt/1000*1000, 10)
	}
	return key
}

func canonicalPanelID(instance, key string) string {
	prefix := instance + ":"
	if !strings.HasPrefix(key, prefix) {
		return key
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), ":")
	if len(parts) != 2 {
		return key
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return key
	}
	created, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || created <= 0 {
		return key
	}
	return panelClientID(instance, parts[0], created)
}

// Re-key legacy fractional timestamps and merge already split identities in
// one transaction. History totals, deletion guards, aliases and the latest
// baseline survive; log checkpoints are independent of client identities.
func (s *Store) migratePanelIdentities() error {
	return s.tx(func(tx *sql.Tx) error {
		var applied string
		if err := tx.QueryRow("SELECT value FROM meta WHERE key='identity_creation_seconds_v1'").Scan(&applied); err == nil {
			return nil
		} else if err != sql.ErrNoRows {
			return err
		}
		rows, err := tx.Query("SELECT id FROM clients ORDER BY id")
		if err != nil {
			return err
		}
		type rename struct{ old, next string }
		changes := []rename{}
		for rows.Next() {
			var old string
			if err = rows.Scan(&old); err != nil {
				rows.Close()
				return err
			}
			if next := canonicalPanelID(s.cfg.InstanceID, old); next != old {
				changes = append(changes, rename{old, next})
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, c := range changes {
			queries := []string{
				`INSERT INTO clients SELECT ?,email,inbound_ids,deleted,up,down,last_seen,has_traffic FROM clients WHERE id=?
				 ON CONFLICT(id) DO UPDATE SET email=excluded.email,inbound_ids=excluded.inbound_ids,deleted=excluded.deleted,up=excluded.up,down=excluded.down,last_seen=excluded.last_seen,has_traffic=excluded.has_traffic
				 WHERE excluded.last_seen>clients.last_seen OR (excluded.last_seen=clients.last_seen AND excluded.deleted<clients.deleted)`,
				`INSERT INTO traffic SELECT time,?,inbound_ids,up,down FROM traffic WHERE client_id=?
				 ON CONFLICT(time,client_id,inbound_ids) DO UPDATE SET up=traffic.up+excluded.up,down=traffic.down+excluded.down`,
				`INSERT INTO visit_counts SELECT time,?,inbound_id,count FROM visit_counts WHERE client_id=?
				 ON CONFLICT(time,client_id,inbound_id) DO UPDATE SET count=visit_counts.count+excluded.count`,
				`INSERT INTO aliases SELECT ?,email,valid_from,valid_to FROM aliases WHERE client_id=?
				 ON CONFLICT(client_id,email,valid_from) DO UPDATE SET valid_to=CASE WHEN aliases.valid_to IS NULL OR excluded.valid_to IS NULL THEN NULL ELSE MAX(aliases.valid_to,excluded.valid_to) END`,
				`INSERT INTO history_deletions SELECT start,end,?,created_at FROM history_deletions WHERE client_id=?
				 ON CONFLICT(start,end,client_id) DO UPDATE SET created_at=MIN(history_deletions.created_at,excluded.created_at)`,
				`UPDATE visits SET client_id=? WHERE client_id=?`,
				`UPDATE quality SET client_id=? WHERE client_id=?`,
			}
			for _, query := range queries {
				if _, err = tx.Exec(query, c.next, c.old); err != nil {
					return err
				}
			}
			for _, table := range []string{"traffic", "visit_counts", "aliases", "history_deletions"} {
				if _, err = tx.Exec("DELETE FROM "+table+" WHERE client_id=?", c.old); err != nil {
					return err
				}
			}
			if _, err = tx.Exec("DELETE FROM clients WHERE id=?", c.old); err != nil {
				return err
			}
		}
		// A split identity must not bypass an existing deletion range when merged.
		for _, c := range changes {
			for _, table := range []string{"visits", "visit_counts", "traffic"} {
				if _, err = tx.Exec("DELETE FROM "+table+" WHERE client_id=? AND EXISTS(SELECT 1 FROM history_deletions h WHERE (h.client_id='' OR h.client_id="+table+".client_id) AND "+table+".time>=h.start AND "+table+".time<h.end)", c.next); err != nil {
					return err
				}
			}
		}
		return setMeta(tx, "identity_creation_seconds_v1", "1")
	})
}
