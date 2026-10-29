package orm

import (
	"reflect"

	"github.com/cupen/libdb/errs"
)

// Identifier 是可选接口，Create/Update/Save 运行时要求实现（接口无法在编译期按方法区分 T）。
type Identifier interface {
	GetID() any
}

// Indexable 是可选接口，供各后端 EnsureIndexes 建索引。
type Indexable interface {
	DBIndex() []Index
}

// IDOf 提取对象 ID。未实现 Identifier 或 ID 为 nil（含 typed nil）时返回
// errs.ErrNoID——空 ID 会写脏数据（sqlite 存 "<nil>"，mongo 自动生成 ObjectID）。
func IDOf(obj any) (any, error) {
	row, ok := obj.(Identifier)
	if !ok {
		return nil, errs.ErrNoID
	}
	id := row.GetID()
	if isNil(id) {
		return nil, errs.ErrNoID
	}
	return id, nil
}

// isNil 同时覆盖 typed nil（interface 里装着 nil 指针时 v != nil 仍成立）。
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
