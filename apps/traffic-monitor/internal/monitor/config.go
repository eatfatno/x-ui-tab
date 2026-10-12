package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Listen                string `json:"listen"`
	Upstream              string `json:"upstream"`
	WebBasePath           string `json:"web_base_path"`
	PanelVersion          string `json:"panel_version"`
	APITokenFile          string `json:"api_token_file"`
	DBPath                string `json:"db_path"`
	AccessLogPath         string `json:"access_log_path"`
	WebDir                string `json:"web_dir"`
	Timezone              string `json:"timezone"`
	SampleIntervalSeconds int    `json:"sample_interval_seconds"`
	DetailDays            int    `json:"detail_days"`
	SummaryDays           int    `json:"summary_days"`
	XrayAPIAddress        string `json:"xray_api_address"`
	LogMaxBytes           int64  `json:"log_max_bytes"`
	InstanceID            string `json:"instance_id"`
	TLSCertFile           string `json:"tls_cert_file,omitempty"`
	TLSKeyFile            string `json:"tls_key_file,omitempty"`
}

func DefaultConfig() Config {
	return Config{Listen: "127.0.0.1:18080", Upstream: "http://127.0.0.1:12053", WebBasePath: "/", PanelVersion: "3.9.0", Timezone: "Asia/Shanghai", SampleIntervalSeconds: 10, DetailDays: 7, SummaryDays: 35, LogMaxBytes: 64 << 20, InstanceID: "local"}
}

func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c *Config) Validate() error {
	u, err := url.Parse(c.Upstream)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("upstream must be an http(s) origin without credentials or a path")
	}
	if _, _, err = net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if strings.ContainsAny(c.WebBasePath, "?#\\\"<> \r\n") || strings.Contains(c.WebBasePath, "..") {
		return errors.New("invalid web_base_path")
	}
	c.WebBasePath = "/" + strings.Trim(c.WebBasePath, "/")
	if c.WebBasePath != "/" {
		c.WebBasePath += "/"
	}
	if c.DetailDays < 1 || c.DetailDays > 7 || c.SummaryDays < c.DetailDays || c.SummaryDays > 35 {
		return errors.New("retention requires 1 <= detail_days <= 7 and detail_days <= summary_days <= 35")
	}
	if c.SampleIntervalSeconds < 1 || c.SampleIntervalSeconds > 300 {
		return errors.New("sample_interval_seconds must be 1..300")
	}
	if c.LogMaxBytes < 4096 {
		return errors.New("log_max_bytes must be at least 4096")
	}
	if c.InstanceID == "" {
		return errors.New("instance_id is required")
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return errors.New("tls_cert_file and tls_key_file must be configured together")
	}
	if c.TLSCertFile != "" && (!filepath.IsAbs(c.TLSCertFile) || !filepath.IsAbs(c.TLSKeyFile)) {
		return errors.New("TLS certificate and key paths must be absolute")
	}
	if _, err = time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	for n, p := range map[string]string{"db_path": c.DBPath, "access_log_path": c.AccessLogPath, "web_dir": c.WebDir, "api_token_file": c.APITokenFile} {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("%s must be absolute", n)
		}
	}
	if c.XrayAPIAddress != "" {
		host, _, e := net.SplitHostPort(c.XrayAPIAddress)
		if e != nil {
			return e
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return errors.New("xray_api_address must be loopback")
		}
	}
	return nil
}

func (c Config) Prefix() string          { return c.WebBasePath + "traffic-monitor/" }
func (c Config) Interval() time.Duration { return time.Duration(c.SampleIntervalSeconds) * time.Second }
