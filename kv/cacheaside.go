package kv

import (
	"context"
	"errors"

	"github.com/cupen/libdb/errs"
)

// CacheAside 是缓存旁路装饰器：cache 提供读加速，src 是权威持久层。
// 读未命中回源并回填；写 src 成功后删 cache（不直接写 cache，避免并发脏值）；
// cache 故障退化为直连 src，src 错误原样返回。
type CacheAside[K Key, V any] struct {
	cache Store[K, V]
	src   Source[K, V]
}

func NewCacheAside[K Key, V any](cache Store[K, V], src Source[K, V]) *CacheAside[K, V] {
	return &CacheAside[K, V]{cache: cache, src: src}
}

func (c *CacheAside[K, V]) Get(ctx context.Context, key K) (V, error) {
	val, err := c.cache.Get(ctx, key)
	if err == nil {
		return val, nil
	}
	if !errors.Is(err, errs.ErrNotFound) {
		// 缓存故障：直连 src
		return c.src.Get(ctx, key)
	}
	val, err = c.src.Get(ctx, key)
	if err != nil {
		return val, err
	}
	_ = c.cache.Set(ctx, key, val)
	return val, nil
}

func (c *CacheAside[K, V]) Set(ctx context.Context, key K, val V) error {
	if err := c.src.Set(ctx, key, val); err != nil {
		return err
	}
	_ = c.cache.Del(ctx, key)
	return nil
}

func (c *CacheAside[K, V]) Del(ctx context.Context, key K) error {
	if err := c.src.Del(ctx, key); err != nil {
		return err
	}
	_ = c.cache.Del(ctx, key)
	return nil
}

func (c *CacheAside[K, V]) Has(ctx context.Context, key K) (bool, error) {
	ok, err := c.cache.Has(ctx, key)
	if err != nil {
		ok = false
	}
	if ok {
		return true, nil
	}
	_, err = c.src.Get(ctx, key)
	if errors.Is(err, errs.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
