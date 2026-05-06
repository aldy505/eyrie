package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLCacheGetOrLoadCachesValue(t *testing.T) {
	cache := newTTLCache[string](time.Minute)

	var calls atomic.Int32
	loader := func() (string, error) {
		calls.Add(1)
		return "cached-value", nil
	}

	first, err := cache.getOrLoad("key", loader)
	if err != nil {
		t.Fatalf("unexpected error on first load: %v", err)
	}
	second, err := cache.getOrLoad("key", loader)
	if err != nil {
		t.Fatalf("unexpected error on second load: %v", err)
	}

	if first != "cached-value" || second != "cached-value" {
		t.Fatalf("unexpected cached values: %q, %q", first, second)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected loader to run once, ran %d times", calls.Load())
	}
}

func TestTTLCacheGetOrLoadExpiresValue(t *testing.T) {
	cache := newTTLCache[string](10 * time.Millisecond)

	var calls atomic.Int32
	loader := func() (string, error) {
		calls.Add(1)
		return "value", nil
	}

	first, err := cache.getOrLoad("key", loader)
	if err != nil {
		t.Fatalf("unexpected error on first load: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	second, err := cache.getOrLoad("key", loader)
	if err != nil {
		t.Fatalf("unexpected error on second load: %v", err)
	}

	if first != second {
		t.Fatalf("expected values to match, got %q and %q", first, second)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected loader to run twice after expiration, ran %d times", calls.Load())
	}
}

func TestTTLCacheSetEvictsExpiredEntries(t *testing.T) {
	cache := newTTLCache[string](10 * time.Millisecond)

	cache.set("expired", "value")
	time.Sleep(20 * time.Millisecond)
	cache.set("fresh", "value")

	if _, ok := cache.entries["expired"]; ok {
		t.Fatal("expected expired entry to be evicted during set")
	}
	if _, ok := cache.entries["fresh"]; !ok {
		t.Fatal("expected fresh entry to remain cached")
	}
	if got := len(cache.entries); got != 1 {
		t.Fatalf("expected exactly 1 cached entry, got %d", got)
	}
}

func TestTTLCacheGetOrLoadDeduplicatesConcurrentMisses(t *testing.T) {
	cache := newTTLCache[int](time.Minute)

	var calls atomic.Int32
	start := make(chan struct{})
	loader := func() (int, error) {
		calls.Add(1)
		<-start
		return 42, nil
	}

	var wg sync.WaitGroup
	results := make(chan int, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := cache.getOrLoad("key", loader)
			if err != nil {
				errs <- err
				return
			}
			results <- value
		}()
	}

	time.Sleep(20 * time.Millisecond)
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	for value := range results {
		if value != 42 {
			t.Fatalf("unexpected cached value %d", value)
		}
	}

	if calls.Load() != 1 {
		t.Fatalf("expected loader to run once, ran %d times", calls.Load())
	}
}

func TestTTLCacheGetOrLoadNilCacheFallsBackToLoader(t *testing.T) {
	var cache *ttlCache[string]
	var calls atomic.Int32

	value, err := cache.getOrLoad("key", func() (string, error) {
		calls.Add(1)
		return "value", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "value" {
		t.Fatalf("unexpected value: %q", value)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected loader to run once, ran %d times", calls.Load())
	}
}

func TestTTLCacheGetOrLoadReturnsLoaderError(t *testing.T) {
	cache := newTTLCache[string](time.Minute)
	boom := errors.New("boom")

	_, err := cache.getOrLoad("key", func() (string, error) {
		return "", boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected %v, got %v", boom, err)
	}
}
