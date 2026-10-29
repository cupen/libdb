package redis

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/cupen/libdb/errs"
)

func testAddr() string {
	if addr := os.Getenv("LIBDB_TEST_REDIS_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:6380"
}

func newTestStore(t *testing.T) *Store[string, testObject] {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: testAddr(), DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		t.Skipf("redis 不可用(%v)，跳过集成测试", err)
	}
	t.Cleanup(func() {
		client.FlushDB(context.Background())
		client.Close()
	})
	return New[string, testObject](client, &Options{Prefix: "libdb_test", TTL: time.Minute})
}

type testObject struct {
	Int int    `json:"int"`
	Str string `json:"str"`
}

func TestRedisStore(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	t.Run("Get/Set", func(t *testing.T) {
		assert.NoError(t, store.Set(ctx, "k1", testObject{Int: 1, Str: "hello"}))
		got, err := store.Get(ctx, "k1")
		assert.NoError(t, err)
		assert.Equal(t, testObject{Int: 1, Str: "hello"}, got)
	})

	t.Run("Get 未命中", func(t *testing.T) {
		_, err := store.Get(ctx, "no_such_key")
		assert.ErrorIs(t, err, errs.ErrNotFound)
	})

	t.Run("Has", func(t *testing.T) {
		assert.NoError(t, store.Set(ctx, "k2", testObject{}))
		ok, err := store.Has(ctx, "k2")
		assert.NoError(t, err)
		assert.True(t, ok)

		ok, err = store.Has(ctx, "no_such_key")
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("Del", func(t *testing.T) {
		assert.NoError(t, store.Set(ctx, "k3", testObject{}))
		assert.NoError(t, store.Del(ctx, "k3"))
		ok, _ := store.Has(ctx, "k3")
		assert.False(t, ok)
		// 删除不存在的 key 不报错
		assert.NoError(t, store.Del(ctx, "k3"))
	})

	t.Run("int64 key", func(t *testing.T) {
		store := New[int64, testObject](store.client, &Options{Prefix: "libdb_test"})
		assert.NoError(t, store.Set(ctx, 42, testObject{Str: "answer"}))
		got, err := store.Get(ctx, 42)
		assert.NoError(t, err)
		assert.Equal(t, "answer", got.Str)
	})
}

// 空 key 应返回错误，而不是 panic。
func TestEmptyKeyReturnsError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	assert.NotPanics(t, func() {
		_, err := store.Get(ctx, "")
		assert.Error(t, err)

		err = store.Set(ctx, "", testObject{})
		assert.Error(t, err)

		err = store.Del(ctx, "")
		assert.Error(t, err)

		_, err = store.Has(ctx, "")
		assert.Error(t, err)
	})
}

// Prefix 必须隔离：同一 client 上不同 Prefix 的 Store 互不可见。
func TestPrefixIsolation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	other := New[string, testObject](store.client, &Options{Prefix: "libdb_test_other"})
	assert.NoError(t, store.Set(ctx, "same", testObject{Str: "a"}))
	assert.NoError(t, other.Set(ctx, "same", testObject{Str: "b"}))

	gotA, err := store.Get(ctx, "same")
	assert.NoError(t, err)
	assert.Equal(t, "a", gotA.Str)

	gotB, err := other.Get(ctx, "same")
	assert.NoError(t, err)
	assert.Equal(t, "b", gotB.Str)

	// 删除一侧不影响另一侧
	assert.NoError(t, other.Del(ctx, "same"))
	ok, err := store.Has(ctx, "same")
	assert.NoError(t, err)
	assert.True(t, ok)
}

// TTL 生效：超过 TTL 后 key 过期，Get 返回 ErrNotFound。
func TestTTLExpiry(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	short := New[string, testObject](store.client, &Options{Prefix: "libdb_test_ttl", TTL: 300 * time.Millisecond})

	assert.NoError(t, short.Set(ctx, "k", testObject{Str: "v"}))
	got, err := short.Get(ctx, "k")
	assert.NoError(t, err)
	assert.Equal(t, "v", got.Str)

	assert.Eventually(t, func() bool {
		_, err := short.Get(ctx, "k")
		return errors.Is(err, errs.ErrNotFound)
	}, 3*time.Second, 100*time.Millisecond, "TTL 到期后应查不到")
}
