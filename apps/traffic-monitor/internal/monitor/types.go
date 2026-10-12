package monitor

import "time"

type Client struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	InboundIDs []int  `json:"inbound_ids"`
	Deleted    bool   `json:"deleted"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	LastSeen   string `json:"last_seen"`
}

type Snapshot struct {
	Client
	HasTraffic bool
}

type Visit struct {
	ID          int64       `json:"id"`
	Time        string      `json:"time"`
	ClientID    string      `json:"client_id"`
	Email       string      `json:"email"`
	Target      string      `json:"target"`
	Destination Destination `json:"destination"`
	Port        int         `json:"port"`
	Protocol    string      `json:"protocol"`
	Inbound     string      `json:"inbound"`
	Outbound    string      `json:"outbound"`
	Status      string      `json:"status"`
	At          time.Time   `json:"-"`
	InboundID   int         `json:"-"`
}

type Query struct {
	Start, End     time.Time
	ClientID       string
	InboundID      int
	Target         string
	Page, PageSize int
}

type Summary struct {
	Up                   int64    `json:"up"`
	Down                 int64    `json:"down"`
	Total                int64    `json:"total"`
	VisitCount           int64    `json:"visit_count"`
	PanelUp              int64    `json:"panel_up"`
	PanelDown            int64    `json:"panel_down"`
	MonitorStartedAt     string   `json:"monitor_started_at"`
	DetailAvailableFrom  string   `json:"detail_available_from"`
	SummaryAvailableFrom string   `json:"summary_available_from"`
	LastVisitAt          string   `json:"last_visit_at,omitempty"`
	Quality              []string `json:"quality"`
	Timezone             string   `json:"timezone"`
}

type TrafficPoint struct {
	Time string `json:"time"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}
type VisitPage struct {
	Items    []Visit `json:"items"`
	Total    int64   `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
	Expired  bool    `json:"expired"`
}
type Health struct {
	Status            string   `json:"status"`
	PanelVersion      string   `json:"panel_version"`
	AdapterCompatible bool     `json:"adapter_compatible"`
	LastSampleAt      string   `json:"last_sample_at"`
	LastLogAt         string   `json:"last_log_at"`
	MonitorStartedAt  string   `json:"monitor_started_at"`
	DetailDays        int      `json:"detail_days"`
	SummaryDays       int      `json:"summary_days"`
	Timezone          string   `json:"timezone"`
	Issues            []string `json:"issues"`
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
