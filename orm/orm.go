// Package orm 提供文档型实体存储抽象。Save 语义同 Upsert；
// Create/Update/Save 要求 T 在运行时实现 Identifier；官方实现见 orm/mongo、orm/sqlite。
package orm

import (
	"context"
	"errors"
	"iter"

	"github.com/cupen/libdb/errs"
)

// Store 是文档型实体存储接口。实现约定：Get/Update 未命中返回 errs.ErrNotFound；
// Create 的 ID 已存在返回 errs.ErrAlreadyExists；Create/Update/Save 的 T 未实现
// Identifier（或 ID 为 nil）返回 errs.ErrNoID；Delete 返回是否真的删除了文档，
// 删不存在的 id 不算错误；ForEach 在调用方 break 时释放底层游标，
// 迭代错误通过 yield 第二返回值传递。
type Store[T any] interface {
	Get(ctx context.Context, id any) (T, error)
	Create(ctx context.Context, obj T) error
	Update(ctx context.Context, obj T) error
	Save(ctx context.Context, obj T) error
	Delete(ctx context.Context, id any) (bool, error)
	List(ctx context.Context, q Query) ([]T, error)
	Count(ctx context.Context, q Query) (int64, error)
	ForEach(ctx context.Context, q Query) iter.Seq2[T, error]
}

// IsNotFound 判断 err 是否为"文档不存在"。
func IsNotFound(err error) bool {
	return errors.Is(err, errs.ErrNotFound)
}
