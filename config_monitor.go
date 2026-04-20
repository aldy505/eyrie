package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/guregu/null/v5"
)

type MonitorType string

const (
	MonitorTypeHTTP     MonitorType = "http"
	MonitorTypeTCP      MonitorType = "tcp"
	MonitorTypeICMP     MonitorType = "icmp"
	MonitorTypeRedis    MonitorType = "redis"
	MonitorTypePostgres MonitorType = "postgres"
)

type MonitorHTTPConfig struct {
	Method              string            `yaml:"method" json:"method" default:"GET"`
	SkipTLSVerify       *bool             `yaml:"skip_tls_verify" json:"skip_tls_verify,omitempty"`
	URL                 string            `yaml:"url" json:"url"`
	Headers             map[string]string `yaml:"headers" json:"headers"`
	ExpectedStatusCodes []int             `yaml:"expected_status_codes" json:"-"`
	TimeoutSeconds      null.Int          `yaml:"timeout_seconds" json:"timeout_seconds"`
	JqAssertion         null.String       `yaml:"jq_assertion" json:"-"`
}

type MonitorTCPConfig struct {
	Address        string      `yaml:"address" json:"address"`
	UseTLS         bool        `yaml:"use_tls" json:"use_tls" default:"false"`
	SkipTLSVerify  bool        `yaml:"skip_tls_verify" json:"skip_tls_verify" default:"false"`
	Send           null.String `yaml:"send" json:"send"`
	ExpectContains null.String `yaml:"expect_contains" json:"expect_contains"`
	TimeoutSeconds null.Int    `yaml:"timeout_seconds" json:"timeout_seconds"`
}

type MonitorICMPConfig struct {
	Host           string   `yaml:"host" json:"host"`
	Count          int      `yaml:"count" json:"count" default:"1"`
	TimeoutSeconds null.Int `yaml:"timeout_seconds" json:"timeout_seconds"`
}

type MonitorRedisConfig struct {
	Address        string      `yaml:"address" json:"address"`
	Password       null.String `yaml:"password" json:"password"`
	Database       int         `yaml:"database" json:"database" default:"0"`
	UseTLS         bool        `yaml:"use_tls" json:"use_tls" default:"false"`
	SkipTLSVerify  bool        `yaml:"skip_tls_verify" json:"skip_tls_verify" default:"false"`
	TimeoutSeconds null.Int    `yaml:"timeout_seconds" json:"timeout_seconds"`
}

type MonitorPostgresConfig struct {
	DSN            string   `yaml:"dsn" json:"dsn"`
	TimeoutSeconds null.Int `yaml:"timeout_seconds" json:"timeout_seconds"`
}

type Monitor struct {
	ID          string      `yaml:"id" json:"id"`
	Name        string      `yaml:"name" json:"name"`
	Description null.String `yaml:"description" json:"description"`
	Interval    string      `yaml:"interval" json:"interval" default:"1m"`
	Type        MonitorType `yaml:"type" json:"type" default:"http"`

	// Legacy top-level HTTP fields are preserved for backwards compatibility.
	Method              string            `yaml:"method" json:"method" default:"GET"`
	SkipTLSVerify       bool              `yaml:"skip_tls_verify" json:"skip_tls_verify" default:"false"`
	Url                 string            `yaml:"url" json:"url"`
	Headers             map[string]string `yaml:"headers" json:"headers"`
	ExpectedStatusCodes []int             `yaml:"expected_status_codes" json:"-"`
	TimeoutSeconds      null.Int          `yaml:"timeout_seconds" json:"timeout_seconds"`
	JqAssertion         null.String       `yaml:"jq_assertion" json:"-"`

	HTTP     *MonitorHTTPConfig    `yaml:"http" json:"http,omitempty"`
	TCP      MonitorTCPConfig      `yaml:"tcp" json:"tcp"`
	ICMP     MonitorICMPConfig     `yaml:"icmp" json:"icmp"`
	Redis    MonitorRedisConfig    `yaml:"redis" json:"redis"`
	Postgres MonitorPostgresConfig `yaml:"postgres" json:"postgres"`
}

type Group struct {
	ID          string      `yaml:"id" json:"id"`
	Name        string      `yaml:"name" json:"name"`
	Description null.String `yaml:"description" json:"description"`
	MonitorIDs  []string    `yaml:"monitor_ids" json:"monitor_ids"`
}

type MonitorConfig struct {
	Monitors []Monitor `yaml:"monitors"`
	Groups   []Group   `yaml:"groups"`
}

func (m Monitor) EffectiveType() MonitorType {
	if m.Type != "" {
		return m.Type
	}
	if m.Postgres.DSN != "" {
		return MonitorTypePostgres
	}
	if m.Redis.Address != "" {
		return MonitorTypeRedis
	}
	if m.ICMP.Host != "" {
		return MonitorTypeICMP
	}
	if m.TCP.Address != "" {
		return MonitorTypeTCP
	}
	return MonitorTypeHTTP
}

func (m Monitor) EffectiveHTTP() MonitorHTTPConfig {
	cfg := MonitorHTTPConfig{}
	if m.HTTP != nil {
		cfg = *m.HTTP
	}
	if cfg.Method == "" {
		cfg.Method = m.Method
	}
	if cfg.Method == "" {
		cfg.Method = "GET"
	}
	if cfg.URL == "" {
		cfg.URL = m.Url
	}
	if cfg.Headers == nil {
		cfg.Headers = m.Headers
	}
	if len(cfg.ExpectedStatusCodes) == 0 {
		cfg.ExpectedStatusCodes = m.ExpectedStatusCodes
	}
	if len(cfg.ExpectedStatusCodes) == 0 {
		cfg.ExpectedStatusCodes = []int{200}
	}
	if !cfg.TimeoutSeconds.Valid {
		cfg.TimeoutSeconds = m.TimeoutSeconds
	}
	if !cfg.JqAssertion.Valid {
		cfg.JqAssertion = m.JqAssertion
	}
	if cfg.SkipTLSVerify == nil {
		legacySkipTLSVerify := m.SkipTLSVerify
		cfg.SkipTLSVerify = &legacySkipTLSVerify
	}
	return cfg
}

func (c MonitorHTTPConfig) SkipTLSVerifyValue() bool {
	return c.SkipTLSVerify != nil && *c.SkipTLSVerify
}

func (m Monitor) EffectiveTimeout(defaultSeconds time.Duration) time.Duration {
	switch m.EffectiveType() {
	case MonitorTypeHTTP:
		if timeout := m.EffectiveHTTP().TimeoutSeconds; timeout.Valid {
			return time.Duration(timeout.Int64) * time.Second
		}
	case MonitorTypeTCP:
		if m.TCP.TimeoutSeconds.Valid {
			return time.Duration(m.TCP.TimeoutSeconds.Int64) * time.Second
		}
	case MonitorTypeICMP:
		if m.ICMP.TimeoutSeconds.Valid {
			return time.Duration(m.ICMP.TimeoutSeconds.Int64) * time.Second
		}
	case MonitorTypeRedis:
		if m.Redis.TimeoutSeconds.Valid {
			return time.Duration(m.Redis.TimeoutSeconds.Int64) * time.Second
		}
	case MonitorTypePostgres:
		if m.Postgres.TimeoutSeconds.Valid {
			return time.Duration(m.Postgres.TimeoutSeconds.Int64) * time.Second
		}
	}
	return defaultSeconds
}

func (m Monitor) IsSuccessfulStatus(statusCode int, explicitSuccess bool) bool {
	switch m.EffectiveType() {
	case MonitorTypeHTTP:
		return slices.Contains(m.EffectiveHTTP().ExpectedStatusCodes, statusCode)
	default:
		return explicitSuccess
	}
}

func (m Monitor) Validate() error {
	switch m.EffectiveType() {
	case MonitorTypeHTTP:
		if m.EffectiveHTTP().URL == "" {
			return fmt.Errorf("monitor %s: http.url is required", m.ID)
		}
	case MonitorTypeTCP:
		if m.TCP.Address == "" {
			return fmt.Errorf("monitor %s: tcp.address is required", m.ID)
		}
	case MonitorTypeICMP:
		if m.ICMP.Host == "" {
			return fmt.Errorf("monitor %s: icmp.host is required", m.ID)
		}
	case MonitorTypeRedis:
		if m.Redis.Address == "" {
			return fmt.Errorf("monitor %s: redis.address is required", m.ID)
		}
	case MonitorTypePostgres:
		if m.Postgres.DSN == "" {
			return fmt.Errorf("monitor %s: postgres.dsn is required", m.ID)
		}
	default:
		return fmt.Errorf("monitor %s: unsupported type %q", m.ID, m.EffectiveType())
	}

	return nil
}
