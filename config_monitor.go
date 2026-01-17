package main

import (
	"github.com/guregu/null/v5"
)

type Monitor struct {
	ID                  string            `yaml:"id" json:"id"`
	Name                string            `yaml:"name" json:"name"`
	Description         null.String       `yaml:"description" json:"description"`
	Interval            string            `yaml:"interval" json:"interval" default:"1m"`
	Method              string            `yaml:"method" json:"method" default:"GET"`
	SkipTLSVerify       bool              `yaml:"skip_tls_verify" json:"skip_tls_verify" default:"false"`
	Url                 string            `yaml:"url" json:"url"`
	Headers             map[string]string `yaml:"headers" json:"headers"`
	ExpectedStatusCodes []int             `yaml:"expected_status_codes" json:"-"`
	TimeoutSeconds      null.Int          `yaml:"timeout_seconds" json:"timeout_seconds"`
	JqAssertion         null.String       `yaml:"jq_assertion" json:"-"`
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
