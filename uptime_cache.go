package main

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type ttlCacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

type ttlCache[T any] struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]ttlCacheEntry[T]
	group   singleflight.Group
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{
		ttl:     ttl,
		entries: make(map[string]ttlCacheEntry[T]),
	}
}

func (c *ttlCache[T]) get(key string) (T, bool) {
	var zero T
	if c == nil {
		return zero, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return zero, false
	}

	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return zero, false
	}

	return entry.value, true
}

func (c *ttlCache[T]) set(key string, value T) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.entries[key] = ttlCacheEntry[T]{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *ttlCache[T]) getOrLoad(key string, loader func() (T, error)) (T, error) {
	if c == nil {
		return loader()
	}

	if value, ok := c.get(key); ok {
		return value, nil
	}

	result, err, _ := c.group.Do(key, func() (any, error) {
		if value, ok := c.get(key); ok {
			return value, nil
		}

		value, err := loader()
		if err != nil {
			return nil, err
		}

		c.set(key, value)
		return value, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}

	return result.(T), nil
}
