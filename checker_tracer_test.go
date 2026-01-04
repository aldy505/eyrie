package main

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"testing"
	"time"
)

func TestNewCheckerTracer(t *testing.T) {
	checkerTracer := NewCheckerTracer()
	if checkerTracer == nil {
		t.Fatal("expected non-nil CheckerTracer")
	}

	clientTrace := checkerTracer.GetClientTrace()
	if clientTrace == nil {
		t.Fatal("expected non-nil ClientTrace")
	}
}

func TestCheckerTracer_GetClientTrace(t *testing.T) {
	checkerTracer := NewCheckerTracer()
	clientTrace := checkerTracer.GetClientTrace()
	if clientTrace == nil {
		t.Fatal("expected non-nil ClientTrace")
	}

	wg := sync.WaitGroup{}

	wg.Go(func() {
		clientTrace.GetConn("example.com:443")
	})
	wg.Go(func() {
		clientTrace.GotConn(httptrace.GotConnInfo{Conn: nil, Reused: false, WasIdle: false, IdleTime: 0})
	})
	wg.Go(func() {
		clientTrace.GotFirstResponseByte()
	})
	wg.Go(func() {
		clientTrace.DNSStart(httptrace.DNSStartInfo{Host: "example.com"})
	})
	wg.Go(func() {
		clientTrace.DNSDone(httptrace.DNSDoneInfo{Addrs: nil, Err: nil})
	})
	wg.Go(func() {
		clientTrace.TLSHandshakeStart()
	})
	wg.Go(func() {
		clientTrace.TLSHandshakeDone(tls.ConnectionState{}, nil)
	})
	wg.Wait()
}

func TestCheckerTracer_GetTimings(t *testing.T) {
	baseTime := time.Now()
	checkerTracer := &CheckerTracer{
		connStartTime:         baseTime,
		connAcquiredTime:      baseTime.Add(50 * time.Millisecond),
		firstResponseByte:     baseTime.Add(150 * time.Millisecond),
		dnsStartTime:          baseTime.Add(10 * time.Millisecond),
		dnsDoneTime:           baseTime.Add(40 * time.Millisecond),
		tlsHandshakeStartTime: baseTime.Add(60 * time.Millisecond),
		tlsHandshakeDoneTime:  baseTime.Add(100 * time.Millisecond),
	}

	timings := checkerTracer.GetTimings()
	if timings.ConnAcquiredMs != 50 {
		t.Errorf("expected ConnAcquiredMs to be 50, got %d", timings.ConnAcquiredMs)
	}

	if timings.FirstResponseByteMs != 100 {
		t.Errorf("expected FirstResponseByteMs to be 100, got %d", timings.FirstResponseByteMs)
	}

	if timings.DNSLookupStartMs != 10 {
		t.Errorf("expected DNSLookupStartMs to be 10, got %d", timings.DNSLookupStartMs)
	}
	if timings.DNSLookupDoneMs != 30 {
		t.Errorf("expected DNSLookupDoneMs to be 30, got %d", timings.DNSLookupDoneMs)
	}

	if timings.TLSHandshakeStartMs != 60 {
		t.Errorf("expected TLSHandshakeStartMs to be 60, got %d", timings.TLSHandshakeStartMs)
	}
	if timings.TLSHandshakeDoneMs != 40 {
		t.Errorf("expected TLSHandshakeDoneMs to be 40, got %d", timings.TLSHandshakeDoneMs)
	}
}
