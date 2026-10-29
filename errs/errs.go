package errs

import "errors"

var (
	// ErrNotFound：key 或文档不存在（Get/Update 未命中）。
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists：Create/Save 遇到 ID 或唯一索引冲突。
	ErrAlreadyExists = errors.New("already exists")

	// ErrNoID：对象未实现 orm.Identifier，或 GetID() 返回 nil。
	ErrNoID = errors.New("no id: object does not implement orm.Identifier")
)
