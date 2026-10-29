package mongo

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/cupen/libdb/errs"
	"github.com/cupen/libdb/orm"
)

func testURI() string {
	if uri := os.Getenv("LIBDB_TEST_MONGO_URI"); uri != "" {
		return uri
	}
	return "mongodb://root:root@127.0.0.1:27018/?authSource=admin"
}

type User struct {
	ID     int64  `bson:"_id"`
	Name   string `bson:"name"`
	Age    int    `bson:"age"`
	Status int    `bson:"status"`
}

func (u User) GetID() any { return u.ID }

func (u User) DBIndex() []orm.Index {
	return []orm.Index{
		{Name: "idx_user_name", Key: []string{"name"}, Unique: true},
		{Key: []string{"-age"}},
	}
}

func newTestStore(t *testing.T) *Store[User] {
	t.Helper()
	client, name := connectOrSkip(t)
	return New[User](client, "libdb_test", name)
}

// nullableUser 的 Status 用指针表达"字段缺失"。
type nullableUser struct {
	ID     int64  `bson:"_id" json:"id"`
	Name   string `bson:"name" json:"name"`
	Status *int   `bson:"status,omitempty" json:"status,omitempty"`
}

func (u nullableUser) GetID() any { return u.ID }

func newNullableTestStore(t *testing.T) *Store[nullableUser] {
	t.Helper()
	client, name := connectOrSkip(t)
	return New[nullableUser](client, "libdb_test", name)
}

// nilIDUser 的 GetID 返回 nil，应被 orm.IDOf 拒绝。
type nilIDUser struct {
	ID int64 `bson:"_id" json:"id"`
}

func (n nilIDUser) GetID() any { return nil }

func newNilIDTestStore(t *testing.T) *Store[nilIDUser] {
	t.Helper()
	client, name := connectOrSkip(t)
	return New[nilIDUser](client, "libdb_test", name)
}

// connectOrSkip 建立连接并在测试结束时清理集合与客户端。
func connectOrSkip(t *testing.T) (*mongo.Client, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := Connect(ctx, testURI())
	if err != nil {
		t.Skipf("mongo 不可用(%v)，跳过集成测试", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("mongo ping 失败(%v)，跳过集成测试", err)
	}
	coll := fmt.Sprintf("user_%s", t.Name())
	t.Cleanup(func() {
		client.Database("libdb_test").Collection(coll).Drop(context.Background())
		client.Disconnect(context.Background())
	})
	return client, coll
}

func seedUsers(t *testing.T, store *Store[User], users ...User) {
	t.Helper()
	for _, u := range users {
		assert.NoError(t, store.Save(context.Background(), u))
	}
}

func TestMongoStore(t *testing.T) {
	ctx := context.Background()

	t.Run("CRUD", func(t *testing.T) {
		store := newTestStore(t)
		u := User{ID: 1, Name: "alice", Age: 20}
		assert.NoError(t, store.Create(ctx, u))
		assert.ErrorIs(t, store.Create(ctx, u), errs.ErrAlreadyExists)

		got, err := store.Get(ctx, int64(1))
		assert.NoError(t, err)
		assert.Equal(t, u, got)

		_, err = store.Get(ctx, int64(999))
		assert.ErrorIs(t, err, errs.ErrNotFound)

		u.Age = 21
		assert.NoError(t, store.Update(ctx, u))
		got, _ = store.Get(ctx, int64(1))
		assert.Equal(t, 21, got.Age)

		missing := User{ID: 999, Name: "nobody"}
		assert.ErrorIs(t, store.Update(ctx, missing), errs.ErrNotFound)

		// Save upsert
		bob := User{ID: 2, Name: "bob", Age: 30}
		assert.NoError(t, store.Save(ctx, bob))
		bob.Age = 31
		assert.NoError(t, store.Save(ctx, bob))
		got, _ = store.Get(ctx, int64(2))
		assert.Equal(t, 31, got.Age)

		ok, err := store.Delete(ctx, int64(1))
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, _ = store.Delete(ctx, int64(1))
		assert.False(t, ok)
	})

	t.Run("List/Count", func(t *testing.T) {
		store := newTestStore(t)
		seedUsers(t, store,
			User{ID: 10, Name: "a", Age: 10, Status: 1},
			User{ID: 11, Name: "b", Age: 20, Status: 1},
			User{ID: 12, Name: "c", Age: 30, Status: 2},
			User{ID: 13, Name: "d", Age: 40, Status: 2},
			User{ID: 14, Name: "e", Age: 50, Status: 2},
		)

		objs, err := store.List(ctx, orm.Q().Eq("status", 2).Sort("-age").Limit(2))
		assert.NoError(t, err)
		assert.Len(t, objs, 2)
		assert.Equal(t, 50, objs[0].Age)
		assert.Equal(t, 40, objs[1].Age)

		objs, err = store.List(ctx, orm.Q().Gt("age", 20).Sort("age").Skip(1))
		assert.NoError(t, err)
		assert.Len(t, objs, 2) // 30,40,50 → skip 1 → 40,50
		assert.Equal(t, 40, objs[0].Age)
		assert.Equal(t, 50, objs[1].Age)

		// 同字段多个条件按 AND 合并，不应互相覆盖
		objs, err = store.List(ctx, orm.Q().Gt("age", 15).Lt("age", 45).Sort("age"))
		assert.NoError(t, err)
		assert.Len(t, objs, 3) // 20,30,40
		assert.Equal(t, 20, objs[0].Age)
		assert.Equal(t, 40, objs[2].Age)

		n, err := store.Count(ctx, orm.Q().In("status", 1, 2))
		assert.NoError(t, err)
		assert.EqualValues(t, 5, n)

		// 空 In 列表匹配不到任何文档，且不应报错
		n, err = store.Count(ctx, orm.Q().In("status"))
		assert.NoError(t, err)
		assert.EqualValues(t, 0, n)
	})

	t.Run("ForEach", func(t *testing.T) {
		store := newTestStore(t)
		seedUsers(t, store,
			User{ID: 10, Name: "a", Age: 10},
			User{ID: 11, Name: "b", Age: 20},
			User{ID: 12, Name: "c", Age: 30},
			User{ID: 13, Name: "d", Age: 40},
		)
		var ages []int
		for obj, err := range store.ForEach(ctx, orm.Q().Sort("age")) {
			assert.NoError(t, err)
			ages = append(ages, obj.Age)
		}
		assert.Equal(t, []int{10, 20, 30, 40}, ages)

		var count int
		for _, err := range store.ForEach(ctx, orm.Q()) {
			assert.NoError(t, err)
			count++
			if count >= 2 {
				break
			}
		}
		assert.Equal(t, 2, count)
	})

	t.Run("EnsureIndexes+唯一索引", func(t *testing.T) {
		store := newTestStore(t)
		assert.NoError(t, store.EnsureIndexes(ctx))
		assert.NoError(t, store.EnsureIndexes(ctx)) // 幂等

		seedUsers(t, store, User{ID: 1, Name: "a", Age: 10})
		err := store.Save(ctx, User{ID: 100, Name: "a"}) // 同名不同 ID
		assert.ErrorIs(t, err, errs.ErrAlreadyExists)

		// Update 路径的唯一冲突同样归一化
		seedUsers(t, store, User{ID: 2, Name: "b"})
		err = store.Update(ctx, User{ID: 2, Name: "a"})
		assert.ErrorIs(t, err, errs.ErrAlreadyExists)
	})

	t.Run("EnsureIndexes 空 Key 返回错误", func(t *testing.T) {
		store := newTestStore(t)
		assert.NotPanics(t, func() {
			err := store.EnsureIndexes(ctx, orm.Index{Key: nil})
			assert.Error(t, err)
		})
	})

	t.Run("逃生舱", func(t *testing.T) {
		store := newTestStore(t)
		assert.NotNil(t, store.Collection())
	})
}

// Mongo 与 sqlite 的缺失字段语义需一致：$ne 匹配缺失字段，Eq nil 匹配缺失字段。
func TestMongoNullSemantics(t *testing.T) {
	ctx := context.Background()
	store := newNullableTestStore(t)
	one := 1
	assert.NoError(t, store.Save(ctx, nullableUser{ID: 1, Name: "a", Status: &one}))
	assert.NoError(t, store.Save(ctx, nullableUser{ID: 2, Name: "b"})) // status 缺失

	t.Run("Ne 匹配缺失字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Ne("status", 1))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(2), objs[0].ID)
	})

	t.Run("Eq nil 匹配缺失字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Eq("status", nil))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(2), objs[0].ID)
	})

	t.Run("同字段区间按 AND 合并", func(t *testing.T) {
		zero := 0
		assert.NoError(t, store.Save(ctx, nullableUser{ID: 3, Name: "c", Status: &zero}))
		// status >= 0 AND status < 1 → 只匹配 status=0 的文档
		objs, err := store.List(ctx, orm.Q().Gte("status", 0).Lt("status", 1))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(3), objs[0].ID)
	})

	// nil 操作数语义（这是 sqlite 侧对齐的基准）：
	// {$ne:null} 只匹配"字段存在且非 null"；$in 含 null 匹配 null 或字段缺失。
	t.Run("Ne nil 只匹配非空字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Ne("status", nil).Sort("_id"))
		assert.NoError(t, err)
		assert.Len(t, objs, 2, "status=1 与 status=0；缺失 status 不匹配")
		assert.Equal(t, int64(1), objs[0].ID)
		assert.Equal(t, int64(3), objs[1].ID)

		nNil, err := store.Count(ctx, orm.Q().Eq("status", nil))
		assert.NoError(t, err)
		nNotNil, err := store.Count(ctx, orm.Q().Ne("status", nil))
		assert.NoError(t, err)
		assert.EqualValues(t, 1, nNil)
		assert.EqualValues(t, 2, nNotNil)
	})

	t.Run("In 含 nil 匹配非空值与缺失字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().In("status", 1, nil).Sort("_id"))
		assert.NoError(t, err)
		assert.Len(t, objs, 2, "status=1 与缺失 status")
		assert.Equal(t, int64(1), objs[0].ID)
		assert.Equal(t, int64(2), objs[1].ID)
	})

	t.Run("In 全 nil 等价于 Eq nil", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().In("status", nil, nil))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(2), objs[0].ID)
	})

	t.Run("In 不含 nil 不匹配缺失字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().In("status", 1))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(1), objs[0].ID)
	})
}

// ForEach 的解码错误要通过 yield 的第二个返回值传播。
func TestMongoForEachErrorPropagation(t *testing.T) {
	ctx := context.Background()
	store := newNullableTestStore(t)
	// 写入一条与实体类型不兼容的文档（status 是字符串，实体期望 *int）
	_, err := store.Collection().InsertOne(ctx, map[string]any{"_id": int64(1), "name": "x", "status": "bad"})
	assert.NoError(t, err)

	var gotErr error
	for _, ev := range store.ForEach(ctx, orm.Q()) {
		if ev != nil {
			gotErr = ev
			break
		}
	}
	assert.Error(t, gotErr)

	_, err = store.List(ctx, orm.Q())
	assert.Error(t, err)
}

func TestMongoNilIDRejected(t *testing.T) {
	ctx := context.Background()
	store := newNilIDTestStore(t)
	assert.ErrorIs(t, store.Create(ctx, nilIDUser{}), errs.ErrNoID)
	assert.ErrorIs(t, store.Save(ctx, nilIDUser{}), errs.ErrNoID)
	assert.ErrorIs(t, store.Update(ctx, nilIDUser{}), errs.ErrNoID)

	n, err := store.Collection().CountDocuments(ctx, map[string]any{})
	assert.NoError(t, err)
	assert.EqualValues(t, 0, n, "不应写入任何脏数据")
}
