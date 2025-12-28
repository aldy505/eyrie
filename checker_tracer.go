package main

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"
)

type CheckerTracer struct {
	sync.Mutex
	connStartTime         time.Time
	connAcquiredTime      time.Time
	firstResponseByte     time.Time
	dnsStartTime          time.Time
	dnsDoneTime           time.Time
	tlsHandshakeStartTime time.Time
	tlsHandshakeDoneTime  time.Time
}

type CheckerTraceTimings struct {
	ConnAcquiredMs      int64 `json:"conn_acquired_ms"`
	FirstResponseByteMs int64 `json:"first_response_byte_ms"`
	DNSLookupStartMs    int64 `json:"dns_lookup_start_ms"`
	DNSLookupDoneMs     int64 `json:"dns_lookup_done_ms"`
	TLSHandshakeStartMs int64 `json:"tls_handshake_start_ms"`
	TLSHandshakeDoneMs  int64 `json:"tls_handshake_done_ms"`
}

func NewCheckerTracer() *CheckerTracer {
	return &CheckerTracer{}
}

func (ct *CheckerTracer) GetClientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GetConn: func(hostPort string) {
			ct.Lock()
			ct.connStartTime = time.Now()
			ct.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			ct.Lock()
			ct.connAcquiredTime = time.Now()
			ct.Unlock()
		},
		GotFirstResponseByte: func() {
			ct.Lock()
			ct.firstResponseByte = time.Now()
			ct.Unlock()
		},
		DNSStart: func(httptrace.DNSStartInfo) {
			ct.Lock()
			ct.dnsStartTime = time.Now()
			ct.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			ct.Lock()
			ct.dnsDoneTime = time.Now()
			ct.Unlock()
		},

		TLSHandshakeStart: func() {
			ct.Lock()
			ct.tlsHandshakeStartTime = time.Now()
			ct.Unlock()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			ct.Lock()
			ct.tlsHandshakeDoneTime = time.Now()
			ct.Unlock()
		},
	}
}

func (ct *CheckerTracer) GetTimings() CheckerTraceTimings {
	ct.Lock()
	defer ct.Unlock()

	var timings CheckerTraceTimings

	if !ct.connAcquiredTime.IsZero() && !ct.connStartTime.IsZero() {
		timings.ConnAcquiredMs = ct.connAcquiredTime.Sub(ct.connStartTime).Milliseconds()
	}

	if !ct.firstResponseByte.IsZero() && !ct.connAcquiredTime.IsZero() {
		timings.FirstResponseByteMs = ct.firstResponseByte.Sub(ct.connAcquiredTime).Milliseconds()
	}

	if !ct.dnsStartTime.IsZero() && !ct.connStartTime.IsZero() {
		timings.DNSLookupStartMs = ct.dnsStartTime.Sub(ct.connStartTime).Milliseconds()
	}

	if !ct.dnsDoneTime.IsZero() && !ct.dnsStartTime.IsZero() {
		timings.DNSLookupDoneMs = ct.dnsDoneTime.Sub(ct.dnsStartTime).Milliseconds()
	}

	if !ct.tlsHandshakeStartTime.IsZero() && !ct.connStartTime.IsZero() {
		timings.TLSHandshakeStartMs = ct.tlsHandshakeStartTime.Sub(ct.connStartTime).Milliseconds()
	}

	if !ct.tlsHandshakeDoneTime.IsZero() && !ct.tlsHandshakeStartTime.IsZero() {
		timings.TLSHandshakeDoneMs = ct.tlsHandshakeDoneTime.Sub(ct.tlsHandshakeStartTime).Milliseconds()
	}
	return timings
}
