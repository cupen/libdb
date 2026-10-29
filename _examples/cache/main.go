// CacheAside 示例：sqlite 作源库 + redis 作缓存。
// 需要先启动 redis：docker compose up -d redis-test（默认 127.0.0.1:6380）
package main

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/cupen/libdb"
	"github.com/cupen/libdb/kv"
	kvredis "github.com/cupen/libdb/kv/redis"
	"github.com/cupen/libdb/orm/sqlite"
)

type Session struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
}

func (s Session) GetID() any { return s.ID }

// sourceAdapter 把 orm.Store 适配成 kv.Source（CacheAside 的源库接口）。
// 只有 Get/Set/Del 三个方法，手写即可。
type sourceAdapter struct {
	store *sqlite.Store[Session]
}

func (a sourceAdapter) Get(ctx context.Context, key string) (Session, error) {
	return a.store.Get(ctx, key)
}

func (a sourceAdapter) Set(ctx context.Context, key string, val Session) error {
	return a.store.Save(ctx, val)
}

func (a sourceAdapter) Del(ctx context.Context, key string) error {
	_, err := a.store.Delete(ctx, key)
	return err
}

func main() {
	ctx := context.Background()

	// 源库：sqlite 文档库
	db := libdb.Must(sqlite.Open("file:cache.db?_pragma=busy_timeout(5000)"))
	srcStore := libdb.Must(sqlite.New[Session](db, "session"))
	src := sourceAdapter{store: srcStore}

	// 缓存：redis（带前缀和 TTL）
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6380"})
	cache := kvredis.New[string, Session](client, &kvredis.Options{
		Prefix: "myapp:Session",
		TTL:    10 * time.Minute,
	})

	store := kv.NewCacheAside[string, Session](cache, src)
	libdb.Must0(store.Set(ctx, "s1", Session{ID: "s1", Owner: "alice"}))

	// 第一次 Get：缓存未命中 → 回源 sqlite 并回填
	// 第二次 Get：直接命中 redis
	s, err := store.Get(ctx, "s1")
	fmt.Println(s.Owner, err)
	s, _ = store.Get(ctx, "s1")
	fmt.Println(s.Owner, err)
}
