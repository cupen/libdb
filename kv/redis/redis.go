// Package redis 用 go-redis/v9 实现 kv.Store，值为 JSON，key 带 Options.Prefix 前缀。
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/cupen/libdb/errs"
	"github.com/cupen/libdb/kv"
)

type Options struct {
	// Prefix 是所有 key 的统一前缀，建议 "应用:类型"，如 "myapp:User"。
	Prefix string

	// TTL 是写入过期时间；0 表示永不过期。
	TTL time.Duration
}

// Store 是 kv.Store 的 redis 实现。
type Store[K kv.Key, V any] struct {
	client goredis.UniversalClient
	prefix string
	ttl    time.Duration
}

var _ kv.Store[string, int] = (*Store[string, int])(nil)

// New 创建存储。client 或 opts 为 nil 属初始化期编程错误，直接 panic。
func New[K kv.Key, V any](client goredis.UniversalClient, opts *Options) *Store[K, V] {
	if client == nil {
		panic("kv/redis: nil client")
	}
	if opts == nil {
		panic("kv/redis: nil options")
	}
	return &Store[K, V]{
		client: client,
		prefix: opts.Prefix,
		ttl:    opts.TTL,
	}
}

// buildKey 拼接前缀与 key；空 key 返回错误而非 panic。
func (s *Store[K, V]) buildKey(key K) (string, error) {
	var idtext string
	switch k := any(key).(type) {
	case string:
		idtext = k
	case int:
		idtext = strconv.FormatInt(int64(k), 10)
	case int32:
		idtext = strconv.FormatInt(int64(k), 10)
	case int64:
		idtext = strconv.FormatInt(k, 10)
	default:
		return "", fmt.Errorf("kv/redis: unsupported key type %T", key)
	}
	if idtext == "" {
		return "", fmt.Errorf("kv/redis: empty key %v", key)
	}
	if s.prefix == "" {
		return idtext, nil
	}
	return s.prefix + ":" + idtext, nil
}

func (s *Store[K, V]) Get(ctx context.Context, key K) (V, error) {
	var val V
	redisKey, err := s.buildKey(key)
	if err != nil {
		return val, err
	}
	data, err := s.client.Get(ctx, redisKey).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return val, errs.ErrNotFound
		}
		return val, err
	}
	if err := json.Unmarshal(data, &val); err != nil {
		return val, err
	}
	return val, nil
}

func (s *Store[K, V]) Set(ctx context.Context, key K, val V) error {
	redisKey, err := s.buildKey(key)
	if err != nil {
		return err
	}
	data, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, redisKey, data, s.ttl).Err()
}

func (s *Store[K, V]) Del(ctx context.Context, key K) error {
	redisKey, err := s.buildKey(key)
	if err != nil {
		return err
	}
	return s.client.Del(ctx, redisKey).Err()
}

func (s *Store[K, V]) Has(ctx context.Context, key K) (bool, error) {
	redisKey, err := s.buildKey(key)
	if err != nil {
		return false, err
	}
	n, err := s.client.Exists(ctx, redisKey).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
