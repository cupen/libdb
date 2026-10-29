package sqlite

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cupen/libdb/errs"
	"github.com/cupen/libdb/orm"
)

type User struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Age    int    `json:"age"`
	Status int    `json:"status"`
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
	db, err := Open("file:libdb_sqlite_test?mode=memory&cache=shared")
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	store, err := New[User](db, fmt.Sprintf("user_%s", t.Name()))
	assert.NoError(t, err)
	return store
}

func seedUsers(t *testing.T, store *Store[User], users ...User) {
	t.Helper()
	for _, u := range users {
		assert.NoError(t, store.Save(context.Background(), u))
	}
}

func TestCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	u := User{ID: 1, Name: "alice", Age: 20}
	assert.NoError(t, store.Create(ctx, u))

	// Create 重复 ID → ErrAlreadyExists
	assert.ErrorIs(t, store.Create(ctx, u), errs.ErrAlreadyExists)

	// Get
	got, err := store.Get(ctx, int64(1))
	assert.NoError(t, err)
	assert.Equal(t, u, got)

	// Get 未命中
	_, err = store.Get(ctx, int64(999))
	assert.ErrorIs(t, err, errs.ErrNotFound)

	// Update
	u.Age = 21
	assert.NoError(t, store.Update(ctx, u))
	got, err = store.Get(ctx, int64(1))
	assert.NoError(t, err)
	assert.Equal(t, 21, got.Age)

	// Update 未命中 → ErrNotFound
	missing := User{ID: 999, Name: "nobody"}
	assert.ErrorIs(t, store.Update(ctx, missing), errs.ErrNotFound)

	// Save upsert：不存在则插入
	bob := User{ID: 2, Name: "bob", Age: 30}
	assert.NoError(t, store.Save(ctx, bob))
	// Save upsert：存在则整体替换
	bob.Age = 31
	assert.NoError(t, store.Save(ctx, bob))
	got, err = store.Get(ctx, int64(2))
	assert.NoError(t, err)
	assert.Equal(t, 31, got.Age)

	// Delete
	ok, err := store.Delete(ctx, int64(1))
	assert.NoError(t, err)
	assert.True(t, ok)
	ok, err = store.Delete(ctx, int64(1))
	assert.NoError(t, err)
	assert.False(t, ok) // 重复删除不算错误
}

type NoIDUser struct {
	Name string
}

func TestNoIdentifier(t *testing.T) {
	db, err := Open("file:libdb_sqlite_test2?mode=memory&cache=shared")
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	store, err := New[NoIDUser](db, "no_id_user")
	assert.NoError(t, err)

	ctx := context.Background()
	obj := NoIDUser{Name: "x"}
	assert.ErrorIs(t, store.Create(ctx, obj), errs.ErrNoID)
	assert.ErrorIs(t, store.Update(ctx, obj), errs.ErrNoID)
	assert.ErrorIs(t, store.Save(ctx, obj), errs.ErrNoID)
}

func TestListQuery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedUsers(t, store,
		User{ID: 1, Name: "a", Age: 10, Status: 1},
		User{ID: 2, Name: "b", Age: 20, Status: 1},
		User{ID: 3, Name: "c", Age: 30, Status: 2},
		User{ID: 4, Name: "d", Age: 40, Status: 2},
		User{ID: 5, Name: "e", Age: 50, Status: 2},
	)

	t.Run("Eq", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Eq("status", 1))
		assert.NoError(t, err)
		assert.Len(t, objs, 2)
	})

	t.Run("In+Sort+Limit+Skip", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().
			In("status", 1, 2).
			Sort("-age").
			Skip(1).
			Limit(2))
		assert.NoError(t, err)
		// age 50,40,30,20,10 → skip 1 → 40,30
		assert.Len(t, objs, 2)
		assert.Equal(t, 40, objs[0].Age)
		assert.Equal(t, 30, objs[1].Age)
	})

	t.Run("Skip 无 Limit", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Sort("age").Skip(3))
		assert.NoError(t, err)
		// age 10,20,30,40,50 → skip 3 → 40,50
		assert.Len(t, objs, 2)
		assert.Equal(t, 40, objs[0].Age)
	})

	t.Run("同字段区间条件", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Gt("age", 15).Lt("age", 45).Sort("age"))
		assert.NoError(t, err)
		// 20,30,40 都满足 AND 语义
		assert.Len(t, objs, 3)
	})

	t.Run("Gt", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Gt("age", 30))
		assert.NoError(t, err)
		assert.Len(t, objs, 2)
	})

	t.Run("Ne", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Ne("status", 2))
		assert.NoError(t, err)
		assert.Len(t, objs, 2)
	})

	t.Run("Count", func(t *testing.T) {
		n, err := store.Count(ctx, orm.Q().Eq("status", 2))
		assert.NoError(t, err)
		assert.EqualValues(t, 3, n)

		n, err = store.Count(ctx, orm.Q())
		assert.NoError(t, err)
		assert.EqualValues(t, 5, n)

		// 空 In 列表匹配不到任何文档
		n, err = store.Count(ctx, orm.Q().In("status"))
		assert.NoError(t, err)
		assert.EqualValues(t, 0, n)
	})

	t.Run("StringEq", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Eq("name", "c"))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(3), objs[0].ID)
	})
}

func TestForEach(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedUsers(t, store,
		User{ID: 1, Name: "a", Age: 10},
		User{ID: 2, Name: "b", Age: 20},
		User{ID: 3, Name: "c", Age: 30},
		User{ID: 4, Name: "d", Age: 40},
	)

	t.Run("遍历全部", func(t *testing.T) {
		var ages []int
		for obj, err := range store.ForEach(ctx, orm.Q().Sort("age")) {
			assert.NoError(t, err)
			ages = append(ages, obj.Age)
		}
		assert.Equal(t, []int{10, 20, 30, 40}, ages)
	})

	t.Run("break 提前终止", func(t *testing.T) {
		var count int
		for obj, err := range store.ForEach(ctx, orm.Q()) {
			assert.NoError(t, err)
			count++
			if obj.Age >= 20 {
				break
			}
		}
		assert.LessOrEqual(t, count, 2)
	})
}

func TestEnsureIndexes(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	assert.NoError(t, store.EnsureIndexes(ctx))

	// 唯一索引生效：同名不同 ID → ErrAlreadyExists
	seedUsers(t, store, User{ID: 1, Name: "alice", Age: 20})
	err := store.Save(ctx, User{ID: 2, Name: "alice", Age: 22})
	assert.ErrorIs(t, err, errs.ErrAlreadyExists)

	// 幂等：重复执行不报错
	assert.NoError(t, store.EnsureIndexes(ctx))
}

// ItemA/ItemB 都声明了自动命名的 name 唯一索引（名称同为 name_1），
// 用于验证不同表之间的索引名隔离。
type ItemA struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (i ItemA) GetID() any { return i.ID }
func (i ItemA) DBIndex() []orm.Index {
	return []orm.Index{{Key: []string{"name"}, Unique: true}}
}

type ItemB struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (i ItemB) GetID() any { return i.ID }
func (i ItemB) DBIndex() []orm.Index {
	return []orm.Index{{Key: []string{"name"}, Unique: true}}
}

// 两张表声明同名 Key 时，索引名必须互相隔离（sqlite 索引名库内全局唯一）。
func TestEnsureIndexesSameKeyAcrossTables(t *testing.T) {
	db, err := Open("file:libdb_sqlite_test3?mode=memory&cache=shared")
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	s1 := libdbMust(New[ItemA](db, "same_key_a"))
	s2 := libdbMust(New[ItemB](db, "same_key_b"))
	assert.NoError(t, s1.EnsureIndexes(ctx))
	assert.NoError(t, s2.EnsureIndexes(ctx))

	assert.NoError(t, s1.Save(ctx, ItemA{ID: 1, Name: "x"}))
	assert.ErrorIs(t, s1.Save(ctx, ItemA{ID: 2, Name: "x"}), errs.ErrAlreadyExists)

	assert.NoError(t, s2.Save(ctx, ItemB{ID: 1, Name: "y"}))
	assert.ErrorIs(t, s2.Save(ctx, ItemB{ID: 2, Name: "y"}), errs.ErrAlreadyExists,
		"同名索引不应因另一张表已创建而被跳过")
}

func libdbMust[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestEscapeHatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedUsers(t, store, User{ID: 1, Name: "alice", Age: 20})

	var n int
	err := store.DB().QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, store.Table())).Scan(&n)
	assert.NoError(t, err)
	assert.Equal(t, 1, n)
}
