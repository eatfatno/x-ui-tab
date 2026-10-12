package monitor

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTargetsBoundedMetadataAndUnmatchedIdentities(t *testing.T) {
	start := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	s, _ := testStore(t, start)
	must(t, s.tx(func(tx *sql.Tx) error {
		for _, email := range []string{"unmatched-a", "unmatched-b"} {
			for i := 99; i >= 0; i-- {
				_, e := tx.Exec("INSERT INTO visits(time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status) VALUES(?,'',?,?,?,'tcp','inbound',1,'direct','accepted')", start.UnixMilli(), email, fmt.Sprintf("host-%03d.github.com", i), 10000+i)
				if e != nil {
					return e
				}
			}
		}
		return nil
	}))
	q := allQuery(start, start.Add(time.Hour))
	q.PageSize = 1
	page, e := s.Targets(q, start.Add(time.Minute))
	must(t, e)
	if page.Total != 2 || len(page.Items) != 1 {
		t.Fatal(page)
	}
	x := page.Items[0]
	if x.VisitCount != 100 || x.TargetCount != 100 || len(x.Targets) != 50 || len(x.Ports) != 50 || x.Targets[0] != "host-000.github.com" || x.Ports[49] != 10049 {
		t.Fatal(x)
	}
	q.Page = 2
	other, e := s.Targets(q, start.Add(time.Minute))
	must(t, e)
	if other.Items[0].Email == x.Email || other.Items[0].Key == x.Key {
		t.Fatal("unmatched clients merged")
	}
}

func TestDomainRulesServicesBoundariesAndIPFallback(t *testing.T) {
	for raw, want := range map[string]string{
		"WWW.BAIDU.COM.": "service:baidu", "avatars.githubusercontent.com": "service:github",
		"r1.googlevideo.com": "service:youtube", "www.youtube.com": "service:youtube",
		"video.douyinvod.com": "service:douyin", "www.tiktok.com": "service:tiktok",
		"chatgpt.com": "service:openai", "github.evil.test": "domain:github.evil.test",
		"notgithub.com": "domain:notgithub.com", "142.250.1.1": "ip:142.250.1.1",
		"2001:db8::1": "ip:2001:db8::1", "localhost": "local:localhost", "127.0.0.1": "local:localhost", "[::1]": "local:localhost",
		"github-cloud.s3.amazonaws.com": "service:github",
	} {
		if got := IdentifyDestination(raw); got.Key != want {
			t.Errorf("%s => %+v; want %s", raw, got, want)
		}
	}
	if IdentifyDestination("nested.github-cloud.s3.amazonaws.com").Key == "service:github" {
		t.Fatal("full rule incorrectly matched a subdomain")
	}
	if domainRules.ServiceCount < 1000 || domainRules.RuleCount < 20000 {
		t.Fatal("incomplete embedded rules")
	}
}

func TestAppWebsiteMediaAndExclusiveNetworksShareServices(t *testing.T) {
	for service, hosts := range map[string][]string{
		"youtube":  {"youtube.com", "youtubei.googleapis.com", "r12.googlevideo.com", "i.ytimg.com", "video.google.com"},
		"telegram": {"web.telegram.org", "api.telegram.org", "t.me", "149.154.167.51", "91.108.4.1", "2001:b28:f23d::1", "::ffff:149.154.167.51"},
		"twitter":  {"x.com", "api.twitter.com", "video.twimg.com", "pbs.twimg.com"},
		"openai":   {"chatgpt.com", "ios.chat.openai.com", "android.chat.openai.com", "cdn.oaistatic.com", "files.oaiusercontent.com", "openai-api.arkoselabs.com"},
		"taobao":   {"www.taobao.com", "acs.m.taobao.com", "adash.m.taobao.com", "h5.m.taobao.com", "tb.cn"},
		"wechat":   {"weixin.qq.com", "servicewechat.com", "vweixinthumb.tc.qq.com"},
		"douyin":   {"douyin.com", "video.douyinvod.com", "v5-dy-o-abtest.zjcdn.com"},
		"bilibili": {"www.bilibili.com", "api.bilibili.com", "upos-bstar1-mirrorakam.akamaized.net"},
	} {
		for _, host := range hosts {
			if got := IdentifyDestination(host); got.Key != "service:"+service {
				t.Errorf("%s => %+v; expected %s", host, got, service)
			}
		}
	}
	for _, host := range []string{"149.154.159.255", "149.154.176.0", "2001:b28:f23e::1", "203.0.113.7"} {
		if got := IdentifyDestination(host); got.Kind != "ip" {
			t.Errorf("unowned IP mislabeled: %s %+v", host, got)
		}
	}
	for _, host := range []string{"browser-intake-datadoghq.com", "host.livekit.cloud", "turn.livekit.cloud", "unrelated.akamaized.net", "unrelated.azureedge.net"} {
		if got := IdentifyDestination(host); got.Key == "service:openai" {
			t.Errorf("shared infrastructure mislabeled OpenAI: %s", host)
		}
	}
	if domainRules.IPRuleCount != 14 || IdentifyDestination("149.154.167.51").Kind != "service_ip" {
		t.Fatal("official IP provenance missing")
	}
}

func TestMinuteBoundsApplyToDetailsTargetsTrafficAndDeletion(t *testing.T) {
	start := time.Date(2026, 10, 9, 1, 30, 0, 0, time.UTC)
	s, _ := testStore(t, start.Add(-time.Minute))
	must(t, s.Sample(start.Add(-time.Minute), []Snapshot{snap("a", "alice", 0, 0)}))
	vs := []Visit{}
	for i, at := range []time.Time{start.Add(-time.Millisecond), start, start.Add(59*time.Second + 999*time.Millisecond), start.Add(time.Minute)} {
		host := "api.telegram.org"
		if i == 2 {
			host = "149.154.167.51"
		}
		vs = append(vs, Visit{At: at, Email: "alice", Target: host, Port: 443, Protocol: "tcp", Inbound: "inbound-1"})
	}
	must(t, s.Ingest("minutes", Checkpoint{Offset: 1000}, vs, start.Add(2*time.Minute), false))
	must(t, s.tx(func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO traffic VALUES(?,'a','[1]',1,2),(?,'a','[1]',10,20),(?,'a','[1]',100,200)", start.Add(-time.Minute).UnixMilli(), start.UnixMilli(), start.Add(time.Minute).UnixMilli())
		return e
	}))
	q := allQuery(start, start.Add(time.Minute))
	now := start.Add(2 * time.Minute)
	visits, e := s.Visits(q, now)
	must(t, e)
	targets, e := s.Targets(q, now)
	must(t, e)
	summary, e := s.Summary(q, now)
	must(t, e)
	traffic, e := s.Traffic(q, now)
	must(t, e)
	preview, e := s.PreviewDeletion(q)
	must(t, e)
	if visits.Total != 2 || targets.Total != 1 || targets.Items[0].VisitCount != 2 || targets.Items[0].TargetCount != 2 || summary.VisitCount != 2 || summary.Total != 30 || len(traffic) != 1 || traffic[0].Up != 10 || preview.Total != 30 || preview.VisitCount != 2 {
		t.Fatalf("minute bounds disagree: %+v %+v %+v %+v %+v", visits, targets, summary, traffic, preview)
	}
	q.Page = 99
	page, e := s.Targets(q, now)
	must(t, e)
	if page.Total != 1 || len(page.Items) != 0 {
		t.Fatal("out of range page lost total", page)
	}
	q.Page = 1
	_, e = s.DeleteHistory(q, preview, now)
	must(t, e)
	all, e := s.Summary(allQuery(start.Add(-time.Minute), start.Add(2*time.Minute)), now)
	must(t, e)
	if all.Total != 303 || all.VisitCount != 2 {
		t.Fatal("adjacent minutes changed", all)
	}
}

func TestTargetsGroupPortsClientsAndFilterByService(t *testing.T) {
	start := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	s, _ := testStore(t, start)
	must(t, s.Sample(start, []Snapshot{snap("a", "alice", 0, 0), snap("b", "bob", 0, 0)}))
	visits := []Visit{}
	for i, raw := range []string{"localhost", "localhost", "127.0.0.1", "www.baidu.com", "bdstatic.com"} {
		visits = append(visits, Visit{At: start.Add(time.Duration(i+1) * time.Second), Email: "alice", Target: raw, Port: 19090 + i%2, Protocol: "tcp", Inbound: "inbound-1", Status: "accepted"})
	}
	visits = append(visits, Visit{At: start.Add(5 * time.Second), Email: "bob", Target: "localhost", Port: 19090, Protocol: "tcp"})
	must(t, s.Ingest("test", Checkpoint{Offset: 123}, visits, start.Add(10*time.Second), false))
	q := allQuery(start, start.Add(time.Hour))
	page, e := s.Targets(q, start.Add(time.Minute))
	must(t, e)
	if page.Total != 3 || len(page.Items) != 3 || page.Items[0].VisitCount != 3 || len(page.Items[0].Ports) != 2 || page.Items[0].ClientID != "a" || page.Items[0].TargetCount != 2 {
		t.Fatalf("grouped: %+v", page)
	}
	q.Target = "百度"
	page, e = s.Targets(q, start.Add(time.Minute))
	must(t, e)
	if page.Total != 1 || page.Items[0].VisitCount != 2 || page.Items[0].Destination.Name != "百度" {
		t.Fatalf("name query %+v", page)
	}
	details, e := s.Visits(q, start.Add(time.Minute))
	must(t, e)
	if details.Total != 2 || details.Items[0].Target == "百度" || details.Items[0].Destination.Name != "百度" {
		t.Fatalf("raw target or name filtering %+v", details)
	}
	q.Target = ""
	q.PageSize = 1
	q.Page = 2
	page, e = s.Targets(q, start.Add(time.Minute))
	must(t, e)
	if page.Total != 3 || len(page.Items) != 1 || page.Items[0].VisitCount != 2 {
		t.Fatal("pagination before aggregation", page)
	}
	page, e = s.Targets(q, start.AddDate(0, 0, 8))
	must(t, e)
	if page.Total != 0 || !page.Expired {
		t.Fatal("expired destinations retained", page)
	}
	x, e := s.Summary(q, start.AddDate(0, 0, 8))
	must(t, e)
	if x.VisitCount != 6 || !strings.Contains(strings.Join(x.Quality, ","), "detail_expired") {
		t.Fatal("35-day counts missing", x)
	}
}
