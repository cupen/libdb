package orm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cupen/libdb/errs"
)

type plainRow struct {
	Name string
}

type nilIDRow struct {
	ID int64
}

func (n nilIDRow) GetID() any { return nil }

type typedNilIDRow struct {
	ID int64
}

func (n *typedNilIDRow) GetID() any { return (*int)(nil) }

type okRow struct {
	ID int64
}

func (o okRow) GetID() any { return o.ID }

func TestIDOf(t *testing.T) {
	t.Run("正常返回 ID", func(t *testing.T) {
		id, err := IDOf(okRow{ID: 42})
		assert.NoError(t, err)
		assert.Equal(t, int64(42), id)
	})

	t.Run("未实现 Identifier", func(t *testing.T) {
		_, err := IDOf(plainRow{Name: "x"})
		assert.ErrorIs(t, err, errs.ErrNoID)
	})

	t.Run("GetID 返回 nil", func(t *testing.T) {
		_, err := IDOf(nilIDRow{ID: 1})
		assert.ErrorIs(t, err, errs.ErrNoID)
	})

	t.Run("GetID 返回 typed nil", func(t *testing.T) {
		_, err := IDOf(&typedNilIDRow{ID: 1})
		assert.ErrorIs(t, err, errs.ErrNoID)
	})

	t.Run("nil 接口值", func(t *testing.T) {
		_, err := IDOf(nil)
		assert.ErrorIs(t, err, errs.ErrNoID)
	})
}
