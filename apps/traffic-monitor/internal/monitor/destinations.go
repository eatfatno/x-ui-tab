package monitor

import (
	"cmp"
	"crypto/sha256"
	"database/sql/driver"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"modernc.org/sqlite"
)

//go:embed rules/domains.json
var domainRuleJSON []byte

type Destination struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Kind   string `json:"kind"`
}
type domainRuleSet struct {
	Source        string `json:"source"`
	Commit        string `json:"commit"`
	ServiceCount  int    `json:"service_count"`
	RuleCount     int    `json:"rule_count"`
	IPRuleCount   int    `json:"ip_rule_count"`
	AppRuleCommit string `json:"app_rule_commit"`
	IPRules       []struct {
		CIDR    string `json:"cidr"`
		Service string `json:"service"`
		Source  string `json:"source"`
	} `json:"ip_rules"`
	Services map[string]struct {
		Name   string `json:"name"`
		Domain string `json:"domain"`
	} `json:"services"`
	Suffix map[string]string `json:"suffix"`
	Exact  map[string]string `json:"exact"`
}

var domainRules domainRuleSet
var serviceNetworks []struct {
	prefix  netip.Prefix
	service string
}

// Keep distinct metadata bounded and stable without retaining complete lists.
func insertMetadata[T cmp.Ordered](values []T, value T) []T {
	i, exists := slices.BinarySearch(values, value)
	if exists || i >= 50 {
		return values
	}
	if len(values) < 50 {
		values = append(values, value)
	}
	copy(values[i+1:], values[i:len(values)-1])
	values[i] = value
	return values
}

func init() {
	if e := json.Unmarshal(domainRuleJSON, &domainRules); e != nil {
		panic(e)
	}
	for _, rule := range domainRules.IPRules {
		prefix, e := netip.ParsePrefix(rule.CIDR)
		if e != nil {
			panic(e)
		}
		serviceNetworks = append(serviceNetworks, struct {
			prefix  netip.Prefix
			service string
		}{prefix, rule.Service})
	}
	sqlite.MustRegisterDeterministicScalarFunction("tm_target_key", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v, _ := a[0].(string)
		return IdentifyDestination(v).Key, nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("tm_target_search", 1, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v, _ := a[0].(string)
		d := IdentifyDestination(v)
		return strings.ToLower(v + " " + d.Name + " " + d.Domain + " " + d.Key), nil
	})
}

func IdentifyDestination(raw string) Destination {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if ip, e := netip.ParseAddr(strings.Trim(host, "[]")); e == nil {
		ip = ip.Unmap()
		if ip.IsLoopback() {
			return Destination{Key: "local:localhost", Name: "localhost", Domain: "localhost", Kind: "local"}
		}
		for _, rule := range serviceNetworks {
			if rule.prefix.Contains(ip) {
				meta := domainRules.Services[rule.service]
				return Destination{Key: "service:" + rule.service, Name: meta.Name, Domain: meta.Domain, Kind: "service_ip"}
			}
		}
		return Destination{Key: "ip:" + ip.String(), Name: ip.String(), Domain: ip.String(), Kind: "ip"}
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return Destination{Key: "local:localhost", Name: "localhost", Domain: "localhost", Kind: "local"}
	}
	service := domainRules.Exact[host]
	if service == "" {
		for suffix := host; suffix != ""; {
			if owner := domainRules.Suffix[suffix]; owner != "" {
				service = owner
				break
			}
			idx := strings.IndexByte(suffix, '.')
			if idx < 0 {
				break
			}
			suffix = suffix[idx+1:]
		}
	}
	if service != "" {
		meta := domainRules.Services[service]
		return Destination{Key: "service:" + service, Name: meta.Name, Domain: meta.Domain, Kind: "service"}
	}
	return Destination{Key: "domain:" + host, Name: host, Domain: host, Kind: "domain"}
}

func destinationFilter(q Query) (string, []any) {
	w, a := filter(q, "visits")
	if q.Target != "" {
		w += " AND instr(tm_target_search(target),lower(?))>0"
		a = append(a, q.Target)
	}
	return w, a
}

type TargetSummary struct {
	Key         string      `json:"key"`
	ClientID    string      `json:"client_id"`
	Email       string      `json:"email"`
	Destination Destination `json:"destination"`
	VisitCount  int64       `json:"visit_count"`
	Targets     []string    `json:"targets"`
	TargetCount int64       `json:"target_count"`
	Ports       []int       `json:"ports"`
	Protocols   []string    `json:"protocols"`
	Inbounds    []string    `json:"inbounds"`
	FirstSeen   string      `json:"first_seen"`
	LastSeen    string      `json:"last_seen"`
}
type TargetPage struct {
	Items    []TargetSummary `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Expired  bool            `json:"expired"`
}

func (s *Store) Targets(q Query, now time.Time) (out TargetPage, err error) {
	out = TargetPage{Items: []TargetSummary{}, Page: q.Page, PageSize: q.PageSize, Expired: q.Start.Before(now.AddDate(0, 0, -s.cfg.DetailDays))}
	q = s.clamp(q, s.cfg.DetailDays, now)
	w, args := filter(q, "visits")
	// One scan of the retained visits supplies counts, pagination and metadata.
	// Repeated connections are collapsed before domain classification. SQLite
	// bounds sorting memory; Go retains only the page and 50 values per field.
	query := `WITH raw AS MATERIALIZED (
SELECT client_id,CASE WHEN client_id='' THEN email ELSE '' END AS identity_email,
MAX(email) AS email,target,port,protocol,inbound,COUNT(*) AS n,MIN(time) AS first,MAX(time) AS last
FROM visits WHERE ` + w + ` GROUP BY client_id,identity_email,target,port,protocol,inbound),
classified AS MATERIALIZED (SELECT *,tm_target_key(target) AS destination FROM raw`
	if q.Target != "" {
		query += " WHERE instr(tm_target_search(target),lower(?))>0"
		args = append(args, q.Target)
	}
	query += `), grouped AS MATERIALIZED (
SELECT client_id,identity_email,MAX(email) AS email,MIN(target) AS target,destination,
SUM(n) AS n,COUNT(DISTINCT target) AS hosts,MIN(first) AS first,MAX(last) AS last
FROM classified GROUP BY client_id,identity_email,destination),
paged AS MATERIALIZED (SELECT *,ROW_NUMBER() OVER (ORDER BY n DESC,destination,client_id,email) AS rank
FROM grouped ORDER BY n DESC,destination,client_id,email LIMIT ? OFFSET ?)
SELECT 0 AS kind,0 AS rank,'','','',COUNT(*),0,0,0,0,'','' FROM grouped
UNION ALL SELECT 1,rank,client_id,email,target,n,hosts,first,last,0,'','' FROM paged
UNION ALL SELECT 2,p.rank,c.client_id,c.email,c.target,0,0,0,0,c.port,c.protocol,c.inbound
FROM classified c JOIN paged p ON c.client_id=p.client_id AND c.identity_email=p.identity_email AND c.destination=p.destination
ORDER BY kind,rank`
	args = append(args, q.PageSize, (q.Page-1)*q.PageSize)
	rows, e := s.db.Query(query, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	selected := map[int]int{}
	for rows.Next() {
		var kind, rank, port int
		var client, email, raw, protocol, inbound string
		var count, hosts, first, last int64
		if e = rows.Scan(&kind, &rank, &client, &email, &raw, &count, &hosts, &first, &last, &port, &protocol, &inbound); e != nil {
			return out, e
		}
		switch kind {
		case 0:
			out.Total = count
		case 1:
			x := TargetSummary{ClientID: client, Email: email, VisitCount: count, TargetCount: hosts, Destination: IdentifyDestination(raw), FirstSeen: stamp(time.UnixMilli(first)), LastSeen: stamp(time.UnixMilli(last)), Targets: []string{}, Ports: []int{}, Protocols: []string{}, Inbounds: []string{}}
			identity := client
			if identity == "" {
				identity = "unknown:" + email
			}
			x.Key = fmt.Sprintf("%x", sha256.Sum256([]byte(identity+"\x00"+x.Destination.Key)))
			selected[rank] = len(out.Items)
			out.Items = append(out.Items, x)
		case 2:
			i, ok := selected[rank]
			if !ok {
				return out, fmt.Errorf("missing destination page rank")
			}
			x := &out.Items[i]
			x.Targets = insertMetadata(x.Targets, raw)
			x.Ports = insertMetadata(x.Ports, port)
			x.Protocols = insertMetadata(x.Protocols, protocol)
			x.Inbounds = insertMetadata(x.Inbounds, inbound)
		}
	}
	return out, rows.Err()
}
