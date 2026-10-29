package orm

import "slices"

// Op 是查询条件中的比较操作。
type Op uint8

const (
	OpEq Op = iota
	OpNe
	OpGt
	OpGte
	OpLt
	OpLte
	OpIn
)

// Cond 是单个查询条件，多个 Cond 之间是 AND；比较类操作使用 Val，In 使用 Vals。
type Cond struct {
	Field string
	Op    Op
	Val   any
	Vals  []any
}

// Query 是后端中立的查询描述，零值匹配所有文档：
//
//	orm.Q().Eq("status", 1).Gt("age", 18).Sort("-created_at").Limit(10)
type Query struct {
	conds []Cond
	sorts []string // "-field" 降序，"field" 升序
	skip  int64
	limit int64
}

// Q 创建空 Query。
func Q() Query {
	return Query{}
}

func (q Query) Eq(field string, val any) Query {
	return q.push(Cond{Field: field, Op: OpEq, Val: val})
}

func (q Query) Ne(field string, val any) Query {
	return q.push(Cond{Field: field, Op: OpNe, Val: val})
}

func (q Query) Gt(field string, val any) Query {
	return q.push(Cond{Field: field, Op: OpGt, Val: val})
}

func (q Query) Gte(field string, val any) Query {
	return q.push(Cond{Field: field, Op: OpGte, Val: val})
}

func (q Query) Lt(field string, val any) Query {
	return q.push(Cond{Field: field, Op: OpLt, Val: val})
}

func (q Query) Lte(field string, val any) Query {
	return q.push(Cond{Field: field, Op: OpLte, Val: val})
}

func (q Query) In(field string, vals ...any) Query {
	return q.push(Cond{Field: field, Op: OpIn, Vals: vals})
}

func (q Query) push(c Cond) Query {
	// 必须克隆：Query 是值类型，直接 append 会让链式分支共享底层数组。
	q.conds = append(slices.Clone(q.conds), c)
	return q
}

// Sort 追加排序字段，可多个：Sort("-created_at", "name")。
func (q Query) Sort(fields ...string) Query {
	q.sorts = append(slices.Clone(q.sorts), fields...)
	return q
}

// Skip 跳过前 n 条（偏移量分页）；负数按 0 处理。
func (q Query) Skip(n int64) Query {
	if n < 0 {
		n = 0
	}
	q.skip = n
	return q
}

// Limit 限制返回条数；0 或负数表示不限制，仅对 List/ForEach 生效，Count 忽略。
func (q Query) Limit(n int64) Query {
	if n < 0 {
		n = 0
	}
	q.limit = n
	return q
}

// Conds 返回全部条件与排序字段，供后端翻译。
func (q Query) Conds() []Cond {
	return q.conds
}

// Sorts 返回排序字段列表，供后端翻译。
func (q Query) Sorts() []string {
	return q.sorts
}

// Paging 返回分页参数 (skip, limit)；limit 为 0 表示不限制。
func (q Query) Paging() (skip, limit int64) {
	return q.skip, q.limit
}
