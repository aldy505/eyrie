package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisTTLCacheGetOrLoadPersistsAcrossInstances(t *testing.T) {
	redisServer, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start redis: %v", err)
	}
	defer redisServer.Close()

	config := ServerConfig{}
	config.Cache.Backend = "redis"
	config.Cache.Redis.Address = redisServer.Addr()

	cache1, err := newUptimeAggregateCache[string](config, "uptime:daily", time.Minute)
	if err != nil {
		t.Fatalf("failed to create redis cache: %v", err)
	}

	var calls int
	value, err := cache1.getOrLoadContext(context.Background(), "monitor:day", func(context.Context) (string, error) {
		calls++
		return "cached-value", nil
	})
	if err != nil {
		t.Fatalf("unexpected error on first load: %v", err)
	}
	if value != "cached-value" {
		t.Fatalf("unexpected cached value: %q", value)
	}
	if calls != 1 {
		t.Fatalf("expected loader to run once, ran %d times", calls)
	}

	if err := cache1.close(); err != nil {
		t.Fatalf("failed to close first cache: %v", err)
	}

	cache2, err := newUptimeAggregateCache[string](config, "uptime:daily", time.Minute)
	if err != nil {
		t.Fatalf("failed to recreate redis cache: %v", err)
	}
	defer cache2.close()

	value, err = cache2.getOrLoadContext(context.Background(), "monitor:day", func(context.Context) (string, error) {
		calls++
		return "should-not-run", nil
	})
	if err != nil {
		t.Fatalf("unexpected error on second load: %v", err)
	}
	if value != "cached-value" {
		t.Fatalf("expected redis-backed cached value, got %q", value)
	}
	if calls != 1 {
		t.Fatalf("expected loader to stay at one call, ran %d times", calls)
	}
}

func TestRedisTTLCacheExpiresEntries(t *testing.T) {
	redisServer, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start redis: %v", err)
	}
	defer redisServer.Close()

	config := ServerConfig{}
	config.Cache.Backend = "redis"
	config.Cache.Redis.Address = redisServer.Addr()

	cache1, err := newUptimeAggregateCache[string](config, "uptime:daily", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to create redis cache: %v", err)
	}

	var calls int
	if _, err := cache1.getOrLoadContext(context.Background(), "monitor:day", func(context.Context) (string, error) {
		calls++
		return "cached-value", nil
	}); err != nil {
		t.Fatalf("unexpected error on first load: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected loader to run once, ran %d times", calls)
	}

	if err := cache1.close(); err != nil {
		t.Fatalf("failed to close first cache: %v", err)
	}

	redisServer.FastForward(20 * time.Millisecond)

	cache2, err := newUptimeAggregateCache[string](config, "uptime:daily", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to recreate redis cache: %v", err)
	}
	defer cache2.close()

	if _, err := cache2.getOrLoadContext(context.Background(), "monitor:day", func(context.Context) (string, error) {
		calls++
		return "refreshed-value", nil
	}); err != nil {
		t.Fatalf("unexpected error on expired reload: %v", err)
	}

	if calls != 2 {
		t.Fatalf("expected loader to run twice after expiration, ran %d times", calls)
	}
}
