package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

type uptimeAggregateCache[T any] interface {
	getOrLoadContext(ctx context.Context, key string, loader func(context.Context) (T, error)) (T, error)
	close() error
}

func newUptimeAggregateCache[T any](config ServerConfig, namespace string, ttl time.Duration) (uptimeAggregateCache[T], error) {
	switch strings.ToLower(strings.TrimSpace(config.Cache.Backend)) {
	case "", "memory":
		return newTTLCache[T](ttl), nil
	case "redis":
		client := redis.NewClient(&redis.Options{
			Addr:         config.Cache.Redis.Address,
			Username:     config.Cache.Redis.Username,
			Password:     config.Cache.Redis.Password,
			DB:           config.Cache.Redis.DB,
			TLSConfig:    redisTLSConfig(config.Cache.Redis.TLS, config.Cache.Redis.SkipTLSVerify),
			PoolSize:     10,
			MinIdleConns: 1,
		})
		return &redisTTLCache[T]{
			client:    client,
			ttl:       ttl,
			prefix:    config.Cache.Redis.Prefix,
			group:     singleflight.Group{},
			namespace: namespace,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported cache backend %q", config.Cache.Backend)
	}
}

func redisTLSConfig(enabled bool, skipVerify bool) *tls.Config {
	if !enabled {
		return nil
	}

	return &tls.Config{InsecureSkipVerify: skipVerify}
}

type redisTTLCache[T any] struct {
	client    *redis.Client
	ttl       time.Duration
	prefix    string
	namespace string
	group     singleflight.Group
}

func (c *redisTTLCache[T]) cacheKey(key string) string {
	if c == nil {
		return key
	}

	if c.prefix == "" {
		return c.namespace + ":" + key
	}

	return c.prefix + ":" + c.namespace + ":" + key
}

func (c *redisTTLCache[T]) get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	b, err := c.client.Get(ctx, c.cacheKey(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return zero, false, nil
		}
		return zero, false, err
	}

	var value T
	if err := json.Unmarshal(b, &value); err != nil {
		return zero, false, err
	}

	return value, true, nil
}

func (c *redisTTLCache[T]) set(ctx context.Context, key string, value T) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.client.Set(ctx, c.cacheKey(key), b, c.ttl).Err()
}

func (c *redisTTLCache[T]) getOrLoadContext(ctx context.Context, key string, loader func(context.Context) (T, error)) (T, error) {
	if c == nil {
		return loader(ctx)
	}

	if value, ok, err := c.get(ctx, key); err != nil {
		var zero T
		return zero, err
	} else if ok {
		return value, nil
	}

	result, err, _ := c.group.Do(key, func() (any, error) {
		if value, ok, err := c.get(ctx, key); err != nil {
			return nil, err
		} else if ok {
			return value, nil
		}

		value, err := loader(ctx)
		if err != nil {
			return nil, err
		}

		if err := c.set(ctx, key, value); err != nil {
			return nil, err
		}

		return value, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}

	return result.(T), nil
}

func (c *redisTTLCache[T]) close() error {
	if c == nil || c.client == nil {
		return nil
	}

	return c.client.Close()
}
