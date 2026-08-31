package server_test

import (
	"context"
	"fmt"
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

// TestGoRedisNewCommands exercises the expanded command surface through the
// official go-redis client: MGET/MSET/MSETNX/GETSET/SETNX/STRLEN/INCRBY/
// DECRBY/EXPIREAT/PEXPIRE/PERSIST/KEYS/SCAN.
func TestGoRedisNewCommands(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	addr := startServer(t, cfg)

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()

	// MSET then MGET with a missing key → redis.Nil slot.
	require.NoError(t, rdb.MSet(ctx, "a", "1", "b", "2").Err())
	vals, err := rdb.MGet(ctx, "a", "b", "missing").Result()
	require.NoError(t, err)
	require.Len(t, vals, 3)
	require.Equal(t, "1", vals[0])
	require.Equal(t, "2", vals[1])
	require.Equal(t, nil, vals[2])

	// MSETNX: all-new keys succeed; a collision aborts the whole batch.
	ok, err := rdb.MSetNX(ctx, "c", "3", "d", "4").Result()
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = rdb.MSetNX(ctx, "e", "5", "a", "99").Result()
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, int64(0), rdb.Exists(ctx, "e").Val())

	// GETSET returns the old value.
	old, err := rdb.GetSet(ctx, "a", "10").Result()
	require.NoError(t, err)
	require.Equal(t, "1", old)
	require.Equal(t, "10", rdb.Get(ctx, "a").Val())

	// SETNX: first sets, second does not.
	require.True(t, rdb.SetNX(ctx, "n1", "v", 0).Val())
	require.False(t, rdb.SetNX(ctx, "n1", "v2", 0).Val())
	require.Equal(t, "v", rdb.Get(ctx, "n1").Val())

	// STRLEN.
	require.NoError(t, rdb.Set(ctx, "lenk", "hello", 0).Err())
	require.Equal(t, int64(5), rdb.StrLen(ctx, "lenk").Val())
	require.Equal(t, int64(0), rdb.StrLen(ctx, "missing").Val())

	// INCRBY / DECRBY.
	require.NoError(t, rdb.Set(ctx, "n", "10", 0).Err())
	require.Equal(t, int64(15), rdb.IncrBy(ctx, "n", 5).Val())
	require.Equal(t, int64(12), rdb.DecrBy(ctx, "n", 3).Val())

	// EXPIREAT in the future keeps the key; in the past deletes it.
	require.True(t, rdb.ExpireAt(ctx, "n", time.Now().Add(time.Hour)).Val())
	require.Equal(t, int64(1), rdb.Exists(ctx, "n").Val())
	require.True(t, rdb.ExpireAt(ctx, "n", time.Now().Add(-time.Hour)).Val())
	require.Equal(t, int64(0), rdb.Exists(ctx, "n").Val())

	// PEXPIRE then PERSIST.
	require.NoError(t, rdb.Set(ctx, "p", "v", 0).Err())
	require.True(t, rdb.PExpire(ctx, "p", time.Minute).Val())
	require.True(t, rdb.Persist(ctx, "p").Val())
	require.Equal(t, time.Duration(-1), rdb.TTL(ctx, "p").Val())

	// KEYS with a glob pattern (order-independent check).
	rdb.MSet(ctx, "user:1", "a", "user:2", "b", "admin", "c")
	keys, err := rdb.Keys(ctx, "user:*").Result()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"user:1", "user:2"}, keys)

	// SCAN cursor iteration collects all keys (go-redis Scan iterates until
	// the server returns cursor 0).
	rdb.FlushDB(ctx)
	for i := 0; i < 20; i++ {
		require.NoError(t, rdb.Set(ctx, fmt.Sprintf("scan:%02d", i), "v", 0).Err())
	}
	var scanned []string
	iter := rdb.Scan(ctx, 0, "scan:*", 5).Iterator()
	for iter.Next(ctx) {
		scanned = append(scanned, iter.Val())
	}
	require.NoError(t, iter.Err())
	require.Len(t, scanned, 20)
}
