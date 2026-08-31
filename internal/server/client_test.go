package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"mem-x/internal/config"
)

// TestGoRedisClientCompat is the integration harness: it drives the running
// server with the official go-redis client to prove RESP wire compatibility
// (not just our own codec talking to itself). Uses the allowlisted external
// deps from AGENTS.md §5.
func TestGoRedisClientCompat(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	addr := startServer(t, cfg)

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()

	// Ping on connect (also exercises the CLIENT SETINFO no-op path).
	require.NoError(t, rdb.Ping(ctx).Err())

	// Set / Get round trip.
	require.NoError(t, rdb.Set(ctx, "key", "value", 0).Err())
	v, err := rdb.Get(ctx, "key").Result()
	require.NoError(t, err)
	require.Equal(t, "value", v)

	// Missing key → redis.Nil.
	_, err = rdb.Get(ctx, "nope").Result()
	require.ErrorIs(t, err, redis.Nil)

	// Integer ops.
	require.NoError(t, rdb.Incr(ctx, "n").Err())
	require.NoError(t, rdb.Incr(ctx, "n").Err())
	n, err := rdb.Incr(ctx, "n").Result()
	require.NoError(t, err)
	require.Equal(t, int64(3), n)

	// Exists / Expire / TTL.
	require.Equal(t, int64(0), rdb.Exists(ctx, "nope").Val())
	require.NoError(t, rdb.Expire(ctx, "key", time.Hour).Err())
	ttl := rdb.TTL(ctx, "key").Val()
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, time.Hour)

	// Append then read back.
	require.NoError(t, rdb.Append(ctx, "key", "-x").Err())
	got, err := rdb.Get(ctx, "key").Result()
	require.NoError(t, err)
	require.Equal(t, "value-x", got)

	// Del / Exists.
	require.NoError(t, rdb.Del(ctx, "key").Err())
	require.Equal(t, int64(0), rdb.Exists(ctx, "key").Val())

	// Type.
	require.NoError(t, rdb.Set(ctx, "tkey", "1", 0).Err())
	require.Equal(t, "string", rdb.Type(ctx, "tkey").Val())

	// Unknown command surfaces as a client error.
	err = rdb.Do(ctx, "BOGUS").Err()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown command")

	// Pipeline of mixed commands stays consistent.
	pipe := rdb.Pipeline()
	pipe.Set(ctx, "p1", "a", 0)
	pipe.Set(ctx, "p2", "b", 0)
	_, err = pipe.Exec(ctx)
	require.NoError(t, err)
	require.Equal(t, "a", rdb.Get(ctx, "p1").Val())
	require.Equal(t, "b", rdb.Get(ctx, "p2").Val())
}
