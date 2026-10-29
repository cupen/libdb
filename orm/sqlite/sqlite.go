// Package sqlite 用 JSON 文档列实现 orm.Store：表固定为 (id TEXT PRIMARY KEY, data TEXT)。
// 查询翻译为 json_extract 表达式，索引翻译为表达式索引；id 统一按文本存储，
// 条件绑定值类型需与 JSON 中类型一致；Index.ExpireAfter 被忽略。
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/cupen/libdb/errs"
	"github.com/cupen/libdb/orm"
)

// Store 是 orm.Store 的 sqlite 实现。
type Store[T any] struct {
	db    *sql.DB
	table string
	null  T
	index []orm.Index
}

var _ orm.Store[int] = (*Store[int])(nil)

// Open 打开连接，driver 已在本包注册。内存库示例：file:test?mode=memory&cache=shared。
func Open(dsn string) (*sql.DB, error) {
	return sql.Open("sqlite", dsn)
}

// New 创建 table 对应的存储，表不存在时自动建表。
func New[T any](db *sql.DB, table string) (*Store[T], error) {
	if db == nil {
		return nil, fmt.Errorf("orm/sqlite: nil db")
	}
	if table == "" {
		return nil, fmt.Errorf("orm/sqlite: empty table name")
	}
	if err := validateIdent("table", table); err != nil {
		return nil, err
	}
	if _, err := db.Exec(fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS "%s" (id TEXT PRIMARY KEY, data TEXT NOT NULL)`, table)); err != nil {
		return nil, err
	}
	var null T
	return &Store[T]{
		db:    db,
		table: table,
		null:  null,
		index: indexesOf(null),
	}, nil
}

// DB 返回原生 *sql.DB（逃生舱）。
func (s *Store[T]) DB() *sql.DB {
	return s.db
}

// Table 返回表名。
func (s *Store[T]) Table() string {
	return s.table
}

func (s *Store[T]) Get(ctx context.Context, id any) (T, error) {
	var val T
	var data string
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT data FROM "%s" WHERE id = ?`, s.table),
		idText(id)).Scan(&data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return val, errs.ErrNotFound
		}
		return val, err
	}
	if err := json.Unmarshal([]byte(data), &val); err != nil {
		return val, err
	}
	return val, nil
}

func (s *Store[T]) Create(ctx context.Context, obj T) error {
	if _, err := orm.IDOf(any(obj)); err != nil {
		return err
	}
	id, data, err := marshalRow(obj)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO "%s" (id, data) VALUES (?, ?)`, s.table),
		id, data); err != nil {
		return conflictAsAlreadyExists(err)
	}
	return nil
}

func (s *Store[T]) Update(ctx context.Context, obj T) error {
	if _, err := orm.IDOf(any(obj)); err != nil {
		return err
	}
	id, data, err := marshalRow(obj)
	if err != nil {
		return err
	}
	rs, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE "%s" SET data = ? WHERE id = ?`, s.table),
		data, id)
	if err != nil {
		return conflictAsAlreadyExists(err)
	}
	n, err := rs.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (s *Store[T]) Save(ctx context.Context, obj T) error {
	if _, err := orm.IDOf(any(obj)); err != nil {
		return err
	}
	id, data, err := marshalRow(obj)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO "%s" (id, data) VALUES (?, ?)
			ON CONFLICT(id) DO UPDATE SET data = excluded.data`, s.table),
		id, data)
	return conflictAsAlreadyExists(err)
}

func (s *Store[T]) Delete(ctx context.Context, id any) (bool, error) {
	rs, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM "%s" WHERE id = ?`, s.table),
		idText(id))
	if err != nil {
		return false, err
	}
	n, err := rs.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store[T]) List(ctx context.Context, q orm.Query) ([]T, error) {
	where, args, err := buildWhere(q)
	if err != nil {
		return nil, err
	}
	orderLimit, err := buildOrderLimit(q)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT data FROM "%s"%s%s`, s.table, where, orderLimit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var objs []T
	for rows.Next() {
		obj, err := scanRow[T](rows)
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj)
	}
	return objs, rows.Err()
}

func (s *Store[T]) Count(ctx context.Context, q orm.Query) (int64, error) {
	where, args, err := buildWhere(q)
	if err != nil {
		return 0, err
	}
	var n int64
	err = s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM "%s"%s`, s.table, where),
		args...).Scan(&n)
	return n, err
}

func (s *Store[T]) ForEach(ctx context.Context, q orm.Query) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		where, args, err := buildWhere(q)
		if err != nil {
			yield(s.null, err)
			return
		}
		orderLimit, err := buildOrderLimit(q)
		if err != nil {
			yield(s.null, err)
			return
		}
		query := fmt.Sprintf(`SELECT data FROM "%s"%s%s`, s.table, where, orderLimit)
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			yield(s.null, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			obj, err := scanRow[T](rows)
			if err != nil {
				yield(obj, err)
				return
			}
			if !yield(obj, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(s.null, err)
		}
	}
}

// EnsureIndexes 把 T 的 DBIndex() 声明翻译为表达式索引（已存在的跳过）。
// Unique/Sparse 支持；ExpireAfter 忽略；Key 方向（- 前缀）仅用于索引命名。
//
// sqlite 的索引名在库内全局唯一（不像 mongo 按 collection 隔离），
// 因此实际索引名统一加表名前缀，避免不同表使用同名 Key 时互相覆盖。
// Key 为空、字段名或索引名非法时返回错误。
func (s *Store[T]) EnsureIndexes(ctx context.Context, extra ...orm.Index) error {
	indexes := append(append([]orm.Index{}, s.index...), extra...)
	for _, idx := range indexes {
		if err := validateIndex(idx); err != nil {
			return err
		}
		var sb strings.Builder
		sb.WriteString("CREATE ")
		if idx.Unique {
			sb.WriteString("UNIQUE ")
		}
		sb.WriteString("INDEX IF NOT EXISTS \"")
		sb.WriteString(s.indexName(idx))
		sb.WriteString(`" ON "`)
		sb.WriteString(s.table)
		sb.WriteString(`" (`)
		for i, f := range idx.Key {
			if i > 0 {
				sb.WriteString(", ")
			}
			field, _ := splitDesc(f)
			expr, _ := jsonExpr(field) // validateIndex 已保证合法
			sb.WriteString(expr)
		}
		sb.WriteString(")")
		if idx.Sparse {
			// 稀疏索引：与 mongo 对齐——至少一个索引键存在就建条目（OR）；
			// 全部键都缺失的行不进入索引。
			sb.WriteString(" WHERE (")
			for i, f := range idx.Key {
				if i > 0 {
					sb.WriteString(" OR ")
				}
				field, _ := splitDesc(f)
				expr, _ := jsonExpr(field)
				sb.WriteString(expr + " IS NOT NULL")
			}
			sb.WriteString(")")
		}
		if _, err := s.db.ExecContext(ctx, sb.String()); err != nil {
			return err
		}
	}
	return nil
}

// indexName 给索引名加上表名前缀，保证库内全局唯一。
func (s *Store[T]) indexName(idx orm.Index) string {
	return s.table + "_" + idx.GetName()
}

func scanRow[T any](rows *sql.Rows) (T, error) {
	var data string
	if err := rows.Scan(&data); err != nil {
		var zero T
		return zero, err
	}
	var obj T
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return obj, err
	}
	return obj, nil
}

// buildWhere 翻译 Query 条件为 WHERE 子句。
//
// NULL 语义刻意与 mongo 对齐（json_extract 对字段缺失和 JSON null 都返回 NULL）：
// Eq(nil)→IS NULL；Ne(nil)→IS NOT NULL；Ne(v)→(IS NULL OR <> ?)，因为 mongo 的
// $ne 匹配缺失字段；In 含 nil→(IN (...) OR IS NULL)，同 mongo $in 含 null。
// Ne 的 OR 形式无法命中表达式索引，大表会全表扫描。
func buildWhere(q orm.Query) (string, []any, error) {
	conds := q.Conds()
	if len(conds) == 0 {
		return "", nil, nil
	}
	var sb strings.Builder
	sb.WriteString(" WHERE ")
	var args []any
	for i, c := range conds {
		if i > 0 {
			sb.WriteString(" AND ")
		}
		expr, err := jsonExpr(c.Field)
		if err != nil {
			return "", nil, err
		}
		switch c.Op {
		case orm.OpEq:
			if c.Val == nil {
				sb.WriteString(expr + " IS NULL")
				continue
			}
			sb.WriteString(expr + " = ?")
			args = append(args, c.Val)
		case orm.OpNe:
			if c.Val == nil {
				sb.WriteString(expr + " IS NOT NULL")
				continue
			}
			sb.WriteString("(" + expr + " IS NULL OR " + expr + " <> ?)")
			args = append(args, c.Val)
		case orm.OpGt:
			sb.WriteString(expr + " > ?")
			args = append(args, c.Val)
		case orm.OpGte:
			sb.WriteString(expr + " >= ?")
			args = append(args, c.Val)
		case orm.OpLt:
			sb.WriteString(expr + " < ?")
			args = append(args, c.Val)
		case orm.OpLte:
			sb.WriteString(expr + " <= ?")
			args = append(args, c.Val)
		case orm.OpIn:
			sb.WriteString(inExpr(expr, c.Vals, &args))
		default:
			return "", nil, fmt.Errorf("orm/sqlite: 不支持的条件操作 %d（字段 %q）", c.Op, c.Field)
		}
	}
	return sb.String(), args, nil
}

// inExpr 对齐 mongo $in 的 null 语义：空列表→不匹配任何行；含 nil→(IN ... OR IS NULL)；全 nil→IS NULL。
func inExpr(expr string, vals []any, args *[]any) string {
	if len(vals) == 0 {
		return "0"
	}
	nonNil := make([]any, 0, len(vals))
	hasNil := false
	for _, v := range vals {
		if v == nil {
			hasNil = true
			continue
		}
		nonNil = append(nonNil, v)
	}
	if !hasNil {
		*args = append(*args, nonNil...)
		return expr + " IN (" + placeholders(len(nonNil)) + ")"
	}
	if len(nonNil) == 0 {
		return expr + " IS NULL"
	}
	*args = append(*args, nonNil...)
	return "(" + expr + " IN (" + placeholders(len(nonNil)) + ") OR " + expr + " IS NULL)"
}

func buildOrderLimit(q orm.Query) (string, error) {
	var sb strings.Builder
	if sorts := q.Sorts(); len(sorts) > 0 {
		sb.WriteString(" ORDER BY ")
		for i, f := range sorts {
			if i > 0 {
				sb.WriteString(", ")
			}
			field, desc := splitDesc(f)
			expr, err := jsonExpr(field)
			if err != nil {
				return "", err
			}
			sb.WriteString(expr)
			if desc {
				sb.WriteString(" DESC")
			}
		}
	}
	skip, limit := q.Paging()
	if limit > 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT %d", limit))
	}
	if skip > 0 {
		if limit <= 0 {
			// sqlite 不接受单独的 OFFSET，跳过剩余全部行写成 LIMIT -1
			sb.WriteString(" LIMIT -1")
		}
		sb.WriteString(fmt.Sprintf(" OFFSET %d", skip))
	}
	return sb.String(), nil
}

// jsonExpr 生成 json_extract 表达式，field 支持 a.b 嵌套路径。
func jsonExpr(field string) (string, error) {
	if err := validateField(field); err != nil {
		return "", err
	}
	return "json_extract(data, '$." + field + "')", nil
}

// validateField 校验 JSON 字段路径，点号用于嵌套，不得在首尾或连续出现。
func validateField(field string) error {
	if err := validateIdent("field", field); err != nil {
		return err
	}
	if strings.HasPrefix(field, ".") || strings.HasSuffix(field, ".") || strings.Contains(field, "..") {
		return fmt.Errorf("orm/sqlite: 非法字段名 %q：点号不能出现在开头/结尾或连续出现", field)
	}
	return nil
}

// validateIdent 校验标识符只含 [A-Za-z0-9_.]，非法字符会拼进 SQL。
func validateIdent(kind, s string) error {
	if s == "" {
		return fmt.Errorf("orm/sqlite: empty %s name", kind)
	}
	pos := -1
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
		default:
			pos = i
		}
		if pos >= 0 {
			break
		}
	}
	if pos >= 0 {
		return fmt.Errorf("orm/sqlite: 非法 %s %q：位置 %d 的字符 %q 不在允许集合 [A-Za-z0-9_.] 内",
			kind, s, pos, s[pos])
	}
	return nil
}

// validateIndex 校验索引定义：Key 非空且字段名、索引名合法。
func validateIndex(idx orm.Index) error {
	if len(idx.Key) == 0 {
		return fmt.Errorf("orm/sqlite: 索引 %s 的 Key 为空", idx.GetName())
	}
	for _, f := range idx.Key {
		field, _ := splitDesc(f)
		if err := validateField(field); err != nil {
			return err
		}
	}
	return validateIndexName(idx.GetName())
}

// validateIndexName 比字段名额外允许 '-'，因为自动生成的索引名带方向后缀（如 age_-1）。
func validateIndexName(name string) error {
	if name == "" {
		return fmt.Errorf("orm/sqlite: empty index name")
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
		default:
			return fmt.Errorf("orm/sqlite: 非法 index %q：位置 %d 的字符 %q 不在允许集合 [A-Za-z0-9_.-] 内",
				name, i, name[i])
		}
	}
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func marshalRow(obj any) (id string, data string, err error) {
	raw, err := orm.IDOf(obj)
	if err != nil {
		return "", "", err
	}
	id = idText(raw)
	buf, err := json.Marshal(obj)
	if err != nil {
		return "", "", err
	}
	return id, string(buf), nil
}

func idText(id any) string {
	switch v := id.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func splitDesc(field string) (string, bool) {
	if strings.HasPrefix(field, "-") {
		return field[1:], true
	}
	return field, false
}

func indexesOf(null any) []orm.Index {
	if row, ok := null.(orm.Indexable); ok {
		return row.DBIndex()
	}
	return nil
}

// conflictAsAlreadyExists 把 SQLITE_CONSTRAINT（主键/唯一索引冲突）归一化为 errs.ErrAlreadyExists。
func conflictAsAlreadyExists(err error) error {
	if err != nil && isConstraint(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func isConstraint(err error) bool {
	var serr interface{ Code() int }
	if errors.As(err, &serr) {
		// 19 = SQLITE_CONSTRAINT, 1555 = SQLITE_CONSTRAINT_PRIMARYKEY,
		// 2067 = SQLITE_CONSTRAINT_UNIQUE
		switch serr.Code() {
		case 19, 1555, 2067:
			return true
		}
	}
	return false
}
