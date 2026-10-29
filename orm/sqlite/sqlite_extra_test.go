package sqlite

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cupen/libdb/errs"
	"github.com/cupen/libdb/orm"
)

// nullableUser 的 Status 用指针表达"字段缺失"，用于对齐 mongo 的 NULL 语义。
type nullableUser struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status *int   `json:"status,omitempty"`
}

func (u nullableUser) GetID() any { return u.ID }

func newNullableStore(t *testing.T, table string) *Store[nullableUser] {
	t.Helper()
	db, err := Open("file:libdb_sqlite_null?mode=memory&cache=shared")
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	store, err := New[nullableUser](db, table)
	assert.NoError(t, err)
	return store
}

// 缺失字段语义：Ne 要匹配"字段缺失"的文档（与 mongo $ne 一致），
// Eq(field, nil) 匹配缺失或 JSON null。
func TestNullSemantics(t *testing.T) {
	store := newNullableStore(t, "null_user_"+t.Name())
	ctx := context.Background()
	one := 1
	assert.NoError(t, store.Save(ctx, nullableUser{ID: 1, Name: "a", Status: &one}))
	assert.NoError(t, store.Save(ctx, nullableUser{ID: 2, Name: "b"})) // status 缺失

	t.Run("Ne 匹配缺失字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Ne("status", 1))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(2), objs[0].ID)
	})

	t.Run("Ne 同时匹配不同值与缺失字段", func(t *testing.T) {
		// mongo $ne:2 会匹配 status=1 与"缺失 status"两类文档
		objs, err := store.List(ctx, orm.Q().Ne("status", 2))
		assert.NoError(t, err)
		assert.Len(t, objs, 2)
	})

	t.Run("Eq nil 匹配缺失字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().Eq("status", nil))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(2), objs[0].ID)
	})

	t.Run("Count 与 List 一致", func(t *testing.T) {
		n, err := store.Count(ctx, orm.Q().Ne("status", 1))
		assert.NoError(t, err)
		assert.EqualValues(t, 1, n)
	})

	// nil 操作数与 mongo 对齐：
	// mongo {$ne:null} 只匹配"字段存在且非 null"的文档。
	t.Run("Ne nil 只匹配非空字段", func(t *testing.T) {
		zero := 0
		assert.NoError(t, store.Save(ctx, nullableUser{ID: 3, Name: "c", Status: &zero}))

		objs, err := store.List(ctx, orm.Q().Ne("status", nil).Sort("id"))
		assert.NoError(t, err)
		assert.Len(t, objs, 2, "status=1 与 status=0 两条，缺失 status 的行不匹配")
		assert.Equal(t, int64(1), objs[0].ID)
		assert.Equal(t, int64(3), objs[1].ID)

		// Ne nil 与 Eq nil 互补，加起来等于全集
		nNil, err := store.Count(ctx, orm.Q().Eq("status", nil))
		assert.NoError(t, err)
		nNotNil, err := store.Count(ctx, orm.Q().Ne("status", nil))
		assert.NoError(t, err)
		assert.EqualValues(t, 1, nNil)
		assert.EqualValues(t, 2, nNotNil)
	})

	// mongo $in 含 null 时匹配 null 与缺失字段。
	t.Run("In 含 nil 匹配非空值与缺失字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().In("status", 1, nil).Sort("id"))
		assert.NoError(t, err)
		assert.Len(t, objs, 2, "status=1 与缺失 status 各行一条")
		assert.Equal(t, int64(1), objs[0].ID)
		assert.Equal(t, int64(2), objs[1].ID)
	})

	t.Run("In 全 nil 等价于 Eq nil", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().In("status", nil, nil))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(2), objs[0].ID)
	})

	t.Run("In 不含 nil 时不匹配缺失字段", func(t *testing.T) {
		objs, err := store.List(ctx, orm.Q().In("status", 1))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(1), objs[0].ID)
	})

	t.Run("In 空列表匹配不到文档", func(t *testing.T) {
		n, err := store.Count(ctx, orm.Q().In("status"))
		assert.NoError(t, err)
		assert.EqualValues(t, 0, n)
	})
}

// 标识符白名单：字段名/表名/索引名不得拼进未校验的 SQL。
func TestIdentifierValidation(t *testing.T) {
	ctx := context.Background()
	db, err := Open("file:libdb_sqlite_inject?mode=memory&cache=shared")
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	store, err := New[User](db, "inject_user")
	assert.NoError(t, err)

	t.Run("拒绝注入字段名", func(t *testing.T) {
		_, err := store.List(ctx, orm.Q().Eq("status) OR 1=1 --", 1))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "非法 field")

		_, err = store.Count(ctx, orm.Q().Eq("id'--", 1))
		assert.Error(t, err)
	})

	t.Run("拒绝注入排序字段", func(t *testing.T) {
		_, err := store.List(ctx, orm.Q().Sort("age; DROP TABLE inject_user"))
		assert.Error(t, err)
	})

	t.Run("拒绝注入表名", func(t *testing.T) {
		_, err := New[User](db, `x"; DROP TABLE inject_user; --`)
		assert.Error(t, err)
	})

	t.Run("拒绝嵌套路径的畸形点号", func(t *testing.T) {
		_, err := store.List(ctx, orm.Q().Eq(".a", 1))
		assert.Error(t, err)
		_, err = store.List(ctx, orm.Q().Eq("a..b", 1))
		assert.Error(t, err)
	})

	t.Run("允许嵌套字段", func(t *testing.T) {
		_, err := store.List(ctx, orm.Q().Eq("meta.owner", "x"))
		assert.NoError(t, err)

		// 嵌套路径要真正生效：写入含 meta.owner 的文档后能命中，换值则不命中
		_, err = store.DB().ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO "%s" (id, data) VALUES (?, ?)`, store.Table()),
			"nested", `{"id":100,"meta":{"owner":"alice"}}`)
		assert.NoError(t, err)

		objs, err := store.List(ctx, orm.Q().Eq("meta.owner", "alice"))
		assert.NoError(t, err)
		assert.Len(t, objs, 1)
		assert.Equal(t, int64(100), objs[0].ID)

		objs, err = store.List(ctx, orm.Q().Eq("meta.owner", "bob"))
		assert.NoError(t, err)
		assert.Empty(t, objs)
	})

	t.Run("拒绝非法索引字段/名称", func(t *testing.T) {
		err := store.EnsureIndexes(ctx, orm.Index{Key: []string{"a); DROP TABLE t; --"}})
		assert.Error(t, err)
	})
}

func TestEnsureIndexesEmptyKey(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	// 空 Key 必须返回错误而不是 panic
	assert.NotPanics(t, func() {
		err := store.EnsureIndexes(ctx, orm.Index{Key: nil})
		assert.Error(t, err)
	})
	assert.NotPanics(t, func() {
		err := store.EnsureIndexes(ctx, orm.Index{Key: []string{}})
		assert.Error(t, err)
	})
}

// Update 路径的唯一索引冲突也要归一化为 ErrAlreadyExists。
func TestUpdateUniqueConflict(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	assert.NoError(t, store.EnsureIndexes(ctx))
	seedUsers(t, store,
		User{ID: 1, Name: "alice", Age: 20},
		User{ID: 2, Name: "bob", Age: 30},
	)
	// 把 bob 改名为 alice → name 唯一索引冲突
	assert.ErrorIs(t, store.Update(ctx, User{ID: 2, Name: "alice", Age: 30}), errs.ErrAlreadyExists)
}

// SparseRow 用于验证多字段 Sparse 索引语义。
type SparseRow struct {
	ID int64  `json:"id"`
	A  string `json:"a,omitempty"`
	B  string `json:"b,omitempty"`
}

func (s SparseRow) GetID() any { return s.ID }

// Sparse 多字段：与 mongo 的过滤条件对齐——至少一个键存在即进索引（OR），
// 两个键都缺失的行不进入索引。
//
// 已知差异：sqlite 的唯一索引把 NULL 视为互不相同，因此"部分键缺失"的行
// （如仅 a=x）之间不会互相冲突；mongo 的复合 sparse 唯一索引会判重（E11000）。
// 全键存在的组合（a=x,b=y）两端行为一致。详见 README。
func TestSparseMultiKey(t *testing.T) {
	db, err := Open("file:libdb_sqlite_sparse?mode=memory&cache=shared")
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	store, err := New[SparseRow](db, "sparse_doc")
	assert.NoError(t, err)
	ctx := context.Background()

	assert.NoError(t, store.EnsureIndexes(ctx,
		orm.Index{Name: "idx_ab", Key: []string{"a", "b"}, Unique: true, Sparse: true}))

	// 全键存在：唯一约束生效（与 mongo 一致）
	assert.NoError(t, store.Save(ctx, SparseRow{ID: 1, A: "x", B: "y"}))
	assert.ErrorIs(t, store.Save(ctx, SparseRow{ID: 2, A: "x", B: "y"}), errs.ErrAlreadyExists)

	// 部分键缺失：两端都进索引；但 sqlite 因 NULL 互不相同而不判重
	// （此处只断言"能写入、不误报冲突"，差异见 README）
	assert.NoError(t, store.Save(ctx, SparseRow{ID: 3, A: "x"}))
	assert.NoError(t, store.Save(ctx, SparseRow{ID: 4, A: "x"}))
	assert.NoError(t, store.Save(ctx, SparseRow{ID: 5, B: "z"}))
	assert.NoError(t, store.Save(ctx, SparseRow{ID: 6, B: "z"}))

	// 两个键都缺失的行不进入稀疏索引，可以重复
	assert.NoError(t, store.Save(ctx, SparseRow{ID: 7}))
	assert.NoError(t, store.Save(ctx, SparseRow{ID: 8}))

	// 索引定义必须是"至少一个键存在"（OR），而不是全部键存在（AND）
	var indexSQL string
	assert.NoError(t, db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`,
		"sparse_doc_idx_ab").Scan(&indexSQL))
	assert.Contains(t, indexSQL, " OR ", "复合 sparse 应为 OR 语义")
	assert.NotContains(t, indexSQL, " AND ")
}

// ForEach 中途的扫描/解码错误要经 yield 的第二个返回值传播，而不是被吞掉。
func TestForEachErrorPropagation(t *testing.T) {
	db, err := Open("file:libdb_sqlite_foreach_err?mode=memory&cache=shared")
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	store, err := New[User](db, "bad_rows")
	assert.NoError(t, err)
	ctx := context.Background()

	// 直接写入一行坏 JSON，模拟损坏数据
	_, err = db.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO "%s" (id, data) VALUES (?, ?)`, store.Table()), "bad", "{not json")
	assert.NoError(t, err)

	var gotErr error
	for _, ev := range store.ForEach(ctx, orm.Q()) {
		if ev != nil {
			gotErr = ev
			break
		}
	}
	assert.Error(t, gotErr)

	// List 同样要报错
	_, err = store.List(ctx, orm.Q())
	assert.Error(t, err)
}

// 非法的 Query 条件名在 ForEach 中也要通过 yield 返回错误。
func TestForEachInvalidField(t *testing.T) {
	store := newTestStore(t)
	var gotErr error
	for _, ev := range store.ForEach(context.Background(), orm.Q().Eq("a b", 1)) {
		if ev != nil {
			gotErr = ev
			break
		}
	}
	assert.Error(t, gotErr)
}

// nil ID 必须被拒绝，不得落库为 "<nil>"。
func TestNilIDRejected(t *testing.T) {
	db, err := Open("file:libdb_sqlite_nilid?mode=memory&cache=shared")
	assert.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	nilStore, err := New[nilIDUser](db, "nil_id_user")
	assert.NoError(t, err)
	ctx := context.Background()
	assert.ErrorIs(t, nilStore.Create(ctx, nilIDUser{}), errs.ErrNoID)
	assert.ErrorIs(t, nilStore.Save(ctx, nilIDUser{}), errs.ErrNoID)

	var n int
	assert.NoError(t, db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, nilStore.Table())).Scan(&n))
	assert.Equal(t, 0, n, "不应写入任何脏行")
}

type nilIDUser struct {
	ID int64 `json:"id"`
}

func (n nilIDUser) GetID() any { return nil }
