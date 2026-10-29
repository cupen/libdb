// Package kv 提供纯键值存储抽象：只有 Get/Set/Del/Has，不含缓存组合等高层语义。
// 需要缓存时用 kv.CacheAside 装饰；官方实现见子包 kv/redis。
package kv

import (
	"context"
	"errors"

	"github.com/cupen/libdb/errs"
)

// IsNotFound 判断 err 是否为"key 不存在"。
func IsNotFound(err error) bool {
	return errors.Is(err, errs.ErrNotFound)
}

// Key 是可作为 KV 主键的基本类型集合。
type Key interface {
	string | int | int32 | int64
}

// Store 是纯 KV 存储接口。实现约定：Get 未命中返回 errs.ErrNotFound；
// Del 删除不存在的 key 不算错误；所有方法尊重 ctx 取消/超时。
type Store[K Key, V any] interface {
	Get(ctx context.Context, key K) (V, error)
	Set(ctx context.Context, key K, val V) error
	Del(ctx context.Context, key K) error
	Has(ctx context.Context, key K) (bool, error)
}

// Source 是 CacheAside 的源库抽象，任意能按 key 读写单值的实现都可用作持久层。
type Source[K Key, V any] interface {
	Get(ctx context.Context, key K) (V, error)
	Set(ctx context.Context, key K, val V) error
	Del(ctx context.Context, key K) error
}
