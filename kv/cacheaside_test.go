package kv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cupen/libdb/errs"
)

type fakeStore[K Key, V any] struct {
	data    map[K]V
	gets    int
	sets    int
	dels    int
	failGet error // 模拟缓存故障
	failSet error // 模拟回填失败
	failDel error // 模拟失效失败
}

func newFakeStore[K Key, V any]() *fakeStore[K, V] {
	return &fakeStore[K, V]{data: map[K]V{}}
}

func (f *fakeStore[K, V]) Get(_ context.Context, key K) (V, error) {
	f.gets++
	var zero V
	if f.failGet != nil {
		return zero, f.failGet
	}
	val, ok := f.data[key]
	if !ok {
		return zero, errs.ErrNotFound
	}
	return val, nil
}

func (f *fakeStore[K, V]) Set(_ context.Context, key K, val V) error {
	f.sets++
	if f.failSet != nil {
		return f.failSet
	}
	f.data[key] = val
	return nil
}

func (f *fakeStore[K, V]) Del(_ context.Context, key K) error {
	f.dels++
	if f.failDel != nil {
		return f.failDel
	}
	delete(f.data, key)
	return nil
}

func (f *fakeStore[K, V]) Has(_ context.Context, key K) (bool, error) {
	if f.failGet != nil {
		return false, f.failGet
	}
	_, ok := f.data[key]
	return ok, nil
}

func TestCacheAside(t *testing.T) {
	ctx := context.Background()

	newPair := func() (*CacheAside[string, int], *fakeStore[string, int], *fakeStore[string, int]) {
		cache := newFakeStore[string, int]()
		src := newFakeStore[string, int]()
		return NewCacheAside[string, int](cache, src), cache, src
	}

	t.Run("Get 未命中回源并回填缓存", func(t *testing.T) {
		ca, cache, src := newPair()
		assert.NoError(t, src.Set(ctx, "a", 1))

		val, err := ca.Get(ctx, "a")
		assert.NoError(t, err)
		assert.Equal(t, 1, val)
		assert.Equal(t, 1, src.gets)

		// 第二次读走缓存
		val, err = ca.Get(ctx, "a")
		assert.NoError(t, err)
		assert.Equal(t, 1, val)
		assert.Equal(t, 1, src.gets)
		assert.Equal(t, 1, cache.sets)
	})

	t.Run("Get 源也未命中 → ErrNotFound", func(t *testing.T) {
		ca, _, _ := newPair()
		_, err := ca.Get(ctx, "missing")
		assert.ErrorIs(t, err, errs.ErrNotFound)
	})

	t.Run("Set 写源库并失效缓存", func(t *testing.T) {
		ca, cache, src := newPair()
		assert.NoError(t, src.Set(ctx, "a", 1))
		_, err := ca.Get(ctx, "a") // 回填缓存
		assert.NoError(t, err)

		assert.NoError(t, ca.Set(ctx, "a", 2))
		_, ok := cache.data["a"]
		assert.False(t, ok, "Set 后缓存应被删除")
		assert.Equal(t, 2, src.data["a"])
	})

	t.Run("Del 删源库并删缓存", func(t *testing.T) {
		ca, cache, src := newPair()
		assert.NoError(t, src.Set(ctx, "a", 1))
		assert.NoError(t, ca.Del(ctx, "a"))
		_, ok := src.data["a"]
		assert.False(t, ok)
		_, ok = cache.data["a"]
		assert.False(t, ok)
	})

	t.Run("缓存故障退化为直连源库", func(t *testing.T) {
		ca, cache, src := newPair()
		cache.failGet = assert.AnError
		assert.NoError(t, src.Set(ctx, "a", 1))

		val, err := ca.Get(ctx, "a")
		assert.NoError(t, err)
		assert.Equal(t, 1, val)
	})

	t.Run("回填失败静默丢弃，不阻断读", func(t *testing.T) {
		ca, cache, src := newPair()
		cache.failSet = assert.AnError
		assert.NoError(t, src.Set(ctx, "a", 1))

		val, err := ca.Get(ctx, "a")
		assert.NoError(t, err, "回填失败不应影响读取结果")
		assert.Equal(t, 1, val)
		assert.Equal(t, 1, cache.sets)
		_, cached := cache.data["a"]
		assert.False(t, cached)

		// 下次读仍然回源（可再次尝试回填）
		val, err = ca.Get(ctx, "a")
		assert.NoError(t, err)
		assert.Equal(t, 1, val)
		assert.Equal(t, 2, src.gets)
	})

	t.Run("Set 时缓存失效失败不阻断写", func(t *testing.T) {
		ca, cache, src := newPair()
		cache.failDel = assert.AnError

		assert.NoError(t, ca.Set(ctx, "a", 2), "缓存失效失败不应影响写源库")
		assert.Equal(t, 2, src.data["a"])
	})

	t.Run("Del 时缓存失效失败不阻断删除", func(t *testing.T) {
		ca, cache, src := newPair()
		cache.failDel = assert.AnError
		assert.NoError(t, src.Set(ctx, "a", 1))

		assert.NoError(t, ca.Del(ctx, "a"))
		_, ok := src.data["a"]
		assert.False(t, ok)
	})

	t.Run("源库错误原样返回", func(t *testing.T) {
		ca, _, src := newPair()
		src.failGet = assert.AnError
		_, err := ca.Get(ctx, "a")
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("Has", func(t *testing.T) {
		ca, _, src := newPair()
		ok, err := ca.Has(ctx, "a")
		assert.NoError(t, err)
		assert.False(t, ok)

		assert.NoError(t, src.Set(ctx, "a", 1))
		ok, err = ca.Has(ctx, "a")
		assert.NoError(t, err)
		assert.True(t, ok)
	})
}
