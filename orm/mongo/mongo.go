// Package mongo 用官方 mongo-driver v2 实现 orm.Store。
// 实体的 ID 字段需映射为 _id（如 bson:"_id"），其余字段按默认 bson 规则编解码。
package mongo

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/cupen/libdb/errs"
	"github.com/cupen/libdb/orm"
)

// Store 是 orm.Store 的 mongodb 实现。
type Store[T any] struct {
	coll  *mongo.Collection
	null  T
	index []orm.Index
}

var _ orm.Store[int] = (*Store[int])(nil)

// Connect 创建客户端。v2 惰性建连，ctx 仅为签名统一保留。
func Connect(ctx context.Context, uri string) (*mongo.Client, error) {
	return mongo.Connect(options.Client().ApplyURI(uri))
}

// New 创建 db.coll 对应的实体存储。
func New[T any](client *mongo.Client, db, coll string) *Store[T] {
	var null T
	return &Store[T]{
		coll:  client.Database(db).Collection(coll),
		null:  null,
		index: indexesOf(null),
	}
}

// Collection 返回原生 *mongo.Collection（复杂查询/聚合的逃生舱）。
func (s *Store[T]) Collection() *mongo.Collection {
	return s.coll
}

func (s *Store[T]) Get(ctx context.Context, id any) (T, error) {
	var obj T
	err := s.coll.FindOne(ctx, bson.M{"_id": id}).Decode(&obj)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return obj, errs.ErrNotFound
		}
		return obj, err
	}
	return obj, nil
}

func (s *Store[T]) Create(ctx context.Context, obj T) error {
	if _, err := orm.IDOf(any(obj)); err != nil {
		return err
	}
	if _, err := s.coll.InsertOne(ctx, obj); err != nil {
		return conflictAsAlreadyExists(err)
	}
	return nil
}

// conflictAsAlreadyExists 把主键/唯一索引冲突（E11000）归一化为
// errs.ErrAlreadyExists，其余错误原样返回。
func conflictAsAlreadyExists(err error) error {
	if err != nil && mongo.IsDuplicateKeyError(err) {
		return errs.ErrAlreadyExists
	}
	return err
}

func (s *Store[T]) Update(ctx context.Context, obj T) error {
	id, err := orm.IDOf(any(obj))
	if err != nil {
		return err
	}
	rs, err := s.coll.ReplaceOne(ctx, bson.M{"_id": id}, obj)
	if err != nil {
		return conflictAsAlreadyExists(err)
	}
	if rs.MatchedCount == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (s *Store[T]) Save(ctx context.Context, obj T) error {
	id, err := orm.IDOf(any(obj))
	if err != nil {
		return err
	}
	_, err = s.coll.ReplaceOne(ctx, bson.M{"_id": id}, obj, options.Replace().SetUpsert(true))
	return conflictAsAlreadyExists(err)
}

func (s *Store[T]) Delete(ctx context.Context, id any) (bool, error) {
	rs, err := s.coll.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return false, err
	}
	return rs.DeletedCount > 0, nil
}

func (s *Store[T]) List(ctx context.Context, q orm.Query) ([]T, error) {
	opts := findOptions(q)
	cur, err := s.coll.Find(ctx, buildFilter(q), opts)
	if err != nil {
		return nil, err
	}
	var objs []T
	if err := cur.All(ctx, &objs); err != nil {
		return nil, err
	}
	return objs, nil
}

func (s *Store[T]) Count(ctx context.Context, q orm.Query) (int64, error) {
	return s.coll.CountDocuments(ctx, buildFilter(q))
}

func (s *Store[T]) ForEach(ctx context.Context, q orm.Query) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		cur, err := s.coll.Find(ctx, buildFilter(q), findOptions(q))
		if err != nil {
			yield(s.null, err)
			return
		}
		defer cur.Close(ctx)
		for cur.Next(ctx) {
			var obj T
			if err := cur.Decode(&obj); err != nil {
				yield(obj, err)
				return
			}
			if !yield(obj, nil) {
				return
			}
		}
		if err := cur.Err(); err != nil {
			yield(s.null, err)
		}
	}
}

// EnsureIndexes 创建 T 的 DBIndex() 声明的索引并追加 extra，同名索引跳过。
func (s *Store[T]) EnsureIndexes(ctx context.Context, extra ...orm.Index) error {
	indexes := append(append([]orm.Index{}, s.index...), extra...)
	if len(indexes) == 0 {
		return nil
	}
	for _, idx := range indexes {
		if len(idx.Key) == 0 {
			return fmt.Errorf("orm/mongo: 索引 %s 的 Key 为空", idx.GetName())
		}
	}
	cur, err := s.coll.Indexes().List(ctx)
	if err != nil {
		return err
	}
	var exists []struct {
		Name string `bson:"name"`
	}
	if err := cur.All(ctx, &exists); err != nil {
		return err
	}
	known := make(map[string]bool, len(exists))
	for _, i := range exists {
		known[i.Name] = true
	}
	for _, idx := range indexes {
		name := idx.GetName()
		if known[name] {
			continue
		}
		if _, err := s.coll.Indexes().CreateOne(ctx, indexModel(idx)); err != nil {
			return err
		}
	}
	return nil
}

func indexesOf(null any) []orm.Index {
	if row, ok := null.(orm.Indexable); ok {
		return row.DBIndex()
	}
	return nil
}

func indexModel(idx orm.Index) mongo.IndexModel {
	builder := options.Index().
		SetName(idx.GetName()).
		SetUnique(idx.Unique).
		SetSparse(idx.Sparse)
	if secs := int64(idx.ExpireAfter / time.Second); secs > 0 {
		builder = builder.SetExpireAfterSeconds(int32(secs))
	}
	return mongo.IndexModel{
		Keys:    parseIndexKeys(idx.Key),
		Options: builder,
	}
}

func parseIndexKeys(fields []string) bson.D {
	var rs bson.D
	for _, f := range fields {
		key, order := parseField(f)
		if key == "" {
			continue
		}
		rs = append(rs, bson.E{Key: key, Value: int32(order)})
	}
	return rs
}

func parseField(field string) (key string, order int32) {
	if len(field) > 0 && field[0] == '-' {
		return field[1:], -1
	}
	return field, 1
}

func buildFilter(q orm.Query) bson.M {
	conds := q.Conds()
	if len(conds) == 0 {
		return bson.M{}
	}
	filter := make(bson.M, len(conds))
	var and []bson.M
	for _, c := range conds {
		expr := condExpr(c)
		prev, exists := filter[c.Field]
		if !exists {
			filter[c.Field] = expr
			continue
		}
		// 同字段多条件（如 Gt+Lt 区间）必须都参与 AND：能合并操作符就合并，
		// 否则下沉 $and，避免后者覆盖前者。
		merged, ok := mergeCond(prev, expr)
		if !ok {
			and = append(and, bson.M{c.Field: expr})
			continue
		}
		filter[c.Field] = merged
	}
	if len(and) > 0 {
		filter["$and"] = and
	}
	return filter
}

func condExpr(c orm.Cond) any {
	switch c.Op {
	case orm.OpEq:
		return c.Val
	case orm.OpNe:
		return bson.M{"$ne": c.Val}
	case orm.OpGt:
		return bson.M{"$gt": c.Val}
	case orm.OpGte:
		return bson.M{"$gte": c.Val}
	case orm.OpLt:
		return bson.M{"$lt": c.Val}
	case orm.OpLte:
		return bson.M{"$lte": c.Val}
	case orm.OpIn:
		vals := c.Vals
		if vals == nil {
			// 必须传空数组而非 null，否则 mongo 报 "$in needs an array"。
			vals = []any{}
		}
		return bson.M{"$in": vals}
	default:
		return bson.M{"$eq": c.Val}
	}
}

// mergeCond 合并同字段的操作符文档（如 $gt+$lt）；无法合并时返回 false 交给 $and。
func mergeCond(prev, next any) (any, bool) {
	pm, ok := prev.(bson.M)
	if !ok {
		return nil, false
	}
	nm, ok := next.(bson.M)
	if !ok {
		return nil, false
	}
	merged := make(bson.M, len(pm)+len(nm))
	for k, v := range pm {
		merged[k] = v
	}
	for k, v := range nm {
		if _, dup := merged[k]; dup {
			return nil, false
		}
		merged[k] = v
	}
	return merged, true
}

func findOptions(q orm.Query) *options.FindOptionsBuilder {
	skip, limit := q.Paging()
	opts := options.Find()
	if limit > 0 {
		opts = opts.SetLimit(limit)
	}
	if skip > 0 {
		opts = opts.SetSkip(skip)
	}
	if sorts := q.Sorts(); len(sorts) > 0 {
		var sortDoc bson.D
		for _, f := range sorts {
			key, order := parseField(f)
			sortDoc = append(sortDoc, bson.E{Key: key, Value: order})
		}
		opts = opts.SetSort(sortDoc)
	}
	return opts
}
