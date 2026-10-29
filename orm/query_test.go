package orm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuery(t *testing.T) {
	t.Run("空 Query 匹配所有", func(t *testing.T) {
		q := Q()
		assert.Empty(t, q.Conds())
		assert.Empty(t, q.Sorts())
		skip, limit := q.Paging()
		assert.EqualValues(t, 0, skip)
		assert.EqualValues(t, 0, limit)
	})

	t.Run("链式条件不可变", func(t *testing.T) {
		base := Q().Eq("status", 1)
		withAge := base.Gt("age", 18)
		// base 不受后续链式调用影响
		assert.Len(t, base.Conds(), 1)
		assert.Len(t, withAge.Conds(), 2)
		assert.Equal(t, OpEq, base.Conds()[0].Op)
		assert.Equal(t, OpGt, withAge.Conds()[1].Op)
	})

	t.Run("兄弟分支不共享底层数组", func(t *testing.T) {
		base := Q().Eq("a", 1).Eq("b", 2).Eq("c", 3)
		x := base.Eq("d", 4)
		y := base.Eq("e", 5)
		assert.Len(t, base.Conds(), 3)
		assert.Equal(t, "d", x.Conds()[3].Field)
		assert.Equal(t, "e", y.Conds()[3].Field, "后一次链式调用不应覆盖前一次")

		sbase := Q().Sort("a")
		sx := sbase.Sort("b")
		sy := sbase.Sort("c")
		assert.Equal(t, []string{"a"}, sbase.Sorts())
		assert.Equal(t, []string{"a", "b"}, sx.Sorts())
		assert.Equal(t, []string{"a", "c"}, sy.Sorts())
	})

	t.Run("In/Sort/Skip/Limit", func(t *testing.T) {
		q := Q().
			In("tag", "a", "b", "c").
			Sort("-created_at", "name").
			Skip(10).
			Limit(5)
		assert.Len(t, q.Conds(), 1)
		assert.Equal(t, []any{"a", "b", "c"}, q.Conds()[0].Vals)
		assert.Equal(t, []string{"-created_at", "name"}, q.Sorts())
		skip, limit := q.Paging()
		assert.EqualValues(t, 10, skip)
		assert.EqualValues(t, 5, limit)
	})

	t.Run("Limit/Skip 负值归一化为 0", func(t *testing.T) {
		q := Q().Limit(-5).Skip(-3)
		skip, limit := q.Paging()
		assert.EqualValues(t, 0, skip)
		assert.EqualValues(t, 0, limit)
	})

	t.Run("比较操作", func(t *testing.T) {
		q := Q().
			Ne("a", 1).
			Gte("b", 2).
			Lt("c", 3).
			Lte("d", 4)
		ops := []Op{}
		for _, c := range q.Conds() {
			ops = append(ops, c.Op)
		}
		assert.Equal(t, []Op{OpNe, OpGte, OpLt, OpLte}, ops)
	})
}
