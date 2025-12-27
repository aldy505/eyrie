package main

import (
	"github.com/guregu/null/v5"
)

type Monitor struct {
	ID                  string            `yaml:"id" json:"id"`
	Name                string            `yaml:"name" json:"name"`
	Interval            string            `yaml:"interval" json:"interval"`
	Method              string            `yaml:"method" json:"method"`
	SkipTLSVerify       bool              `yaml:"skip_tls_verify" json:"skip_tls_verify"`
	Url                 string            `yaml:"url" json:"url"`
	Headers             map[string]string `yaml:"headers" json:"headers"`
	ExpectedStatusCodes []int             `yaml:"expected_status_codes" json:"-"`
	TimeoutSeconds      null.Int          `yaml:"timeout_seconds" json:"timeout_seconds"`
	JqAssertion         null.String       `yaml:"jq_assertion" json:"-"`
}

type MonitorConfig struct {
	Monitors []Monitor `yaml:"monitors"`
}
