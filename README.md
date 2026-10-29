# libdb

Go 的数据库访问抽象库：一套接口，多种后端。

```go
db, _ := sqlite.Open("app.db")                          // 也可用内存库，零外部依赖
users, _ := sqlite.New[User](db, "user")                // 换成 mongo.New[User](client, db, coll) 即换后端
users.Save(ctx, User{ID: 1, Name: "alice"})             // 保存（upsert）
alice, _ := users.Get(ctx, 1)                           // 不存在时返回 errs.ErrNotFound
list, _ := users.List(ctx, orm.Q().Eq("name", "alice")) // 任一后端都是这套 Query
```

- **kv** —— 纯键值存储抽象（`kv.Store[K, V]`：Get/Set/Del/Has），基于 redis 的官方实现（`kv/redis`），以及 `kv.CacheAside` 缓存旁路装饰器
- **orm** —— 文档型实体存储抽象（`orm.Store[T]`：Get/Create/Update/Save/Delete/List/Count/ForEach），官方实现 `orm/mongo`（官方 mongo-driver v2）与 `orm/sqlite`（modernc.org/sqlite，纯 Go 无 cgo，JSON 文档列模型）

```
github.com/cupen/libdb
├── kv/            # kv.Store 接口 + CacheAside 装饰器
│   └── redis/     # redis 实现（JSON 序列化、key 前缀、TTL）
├── orm/           # orm.Store 接口 + Query DSL + Index + Identifier/Indexable
│   ├── mongo/     # mongodb 实现（官方 driver v2）
│   └── sqlite/    # sqlite 实现（JSON 文档列）
└── errs/          # ErrNotFound / ErrAlreadyExists / ErrNoID 哨兵错误
```

## orm：文档型实体存储

```go
type User struct {
    ID     int64  `bson:"_id" json:"id"` // mongo 用 _id；sqlite 按文本主键存
    Name   string `bson:"name" json:"name"`
    Age    int    `bson:"age"   json:"age"`
    Status int    `bson:"status" json:"status"`
}

func (u User) GetID() any            { return u.ID } // 可选接口：Create/Update/Save 需要
func (u User) DBIndex() []orm.Index {                 // 可选接口：EnsureIndexes 使用
    return []orm.Index{{Key: []string{"name"}, Unique: true}}
}
```

sqlite（嵌入式文档库，无外部依赖）：

```go
db, _ := sqlite.Open("file:app.db?_pragma=busy_timeout(5000)")
store, _ := sqlite.New[User](db, "user")
store.EnsureIndexes(ctx)
```

mongo：

```go
client, _ := mongo.Connect(ctx, "mongodb://root:root@127.0.0.1:27018/?authSource=admin")
store := mongo.New[User](client, "mydb", "user")
```

数据库风格的动词集（两个后端行为一致）：

```go
store.Create(ctx, u)             // 已存在 → errs.ErrAlreadyExists
store.Update(ctx, u)             // 不存在 → errs.ErrNotFound；整文档替换
store.Save(ctx, u)               // upsert
store.Delete(ctx, id)            // (bool, error)，删不存在不算错
store.Get(ctx, id)               // 未命中 → errs.ErrNotFound

// 查询 DSL：等值/比较/IN + 排序 + 分页，mongo/sqlite 各自翻译
users, _ := store.List(ctx, orm.Q().Eq("status", 1).Gt("age", 18).Sort("-age").Limit(10))
n, _     := store.Count(ctx, orm.Q().In("status", 1, 2))

// 流式迭代（Go 1.23+ range-over-func），break 即释放游标
for u, err := range store.ForEach(ctx, orm.Q().Sort("age")) {
    if err != nil { return err }
    handle(u)
}

// 逃生舱：复杂查询直连后端原生句柄
store.Collection()  // *mongo.Collection
store.DB()          // *sql.DB
```

## kv：纯键值 + 缓存旁路

```go
client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379"})
cache := kvredis.New[string, User](client, &kvredis.Options{
    Prefix: "myapp:User", // key 前缀，建议 "应用:类型"
    TTL:    10 * time.Minute,
})

cache.Set(ctx, "123", u)
u, err := cache.Get(ctx, "123") // 未命中 → errs.ErrNotFound
cache.Has(ctx, "123")
cache.Del(ctx, "123")
```

给 orm.Store 加 redis 缓存（读回填、写失效；见 `_examples/cache`）：

```go
store := kv.NewCacheAside[string, User](cache, ormSource)
```

`kv.Source` 只有 Get/Set/Del 三个方法，用适配器把 orm.Store 包一层即可。

## 错误语义

所有实现统一：

| 错误 | 含义 |
|---|---|
| `errs.ErrNotFound` | key / 文档不存在（Get、Update 未命中） |
| `errs.ErrAlreadyExists` | ID 或唯一索引冲突（Create、Save、Update） |
| `errs.ErrNoID` | 对象未实现 `GetID() any`，不能用于 Create/Update/Save |

一律用 `errors.Is(err, errs.ErrNotFound)` 判断。

## 已知差异与限制

- sqlite 是 JSON 文档列模型（`id TEXT PRIMARY KEY, data TEXT`），无 schema/迁移/列级查询；`EnsureIndexes` 翻译为表达式索引，`Index.ExpireAfter`（TTL）被忽略（mongo 支持）。
- sqlite 的索引名在库内全局唯一，因此实际索引名带表名前缀（`<table>_<自动名>`），不同表使用相同 Key 不会互相覆盖。
- sqlite 查询条件的参数类型需与 JSON 中存储的类型一致（数字用数字、字符串用字符串）。
- `Update` 是整文档替换：未设置的字段会被清空（两个后端一致）；只想改部分字段时请先 `Get` 再改。
- mongo 实体的 ID 字段需映射 `bson:"_id"`。
- `GetID()` 返回 nil（含 typed nil）或未实现 `orm.Identifier` 时，`Create`/`Update`/`Save` 返回 `errs.ErrNoID`；ID 为空不做任何写入。
- `Query` 条件之间是 AND（同一字段的多个条件也按 AND 合并）；OR、聚合等复杂查询走逃生舱。
- 缺失字段语义与 mongo 对齐：`Ne(f, v)`（v 非 nil）匹配"字段缺失"的文档；`Ne(f, nil)` 只匹配字段存在且非 null（同 `{$ne:null}`）；`Eq(f, nil)` 匹配字段缺失或 JSON null。
- `In` 传入空列表时两个后端都匹配不到任何文档；`In` 列表含 nil 时匹配 nil 或字段缺失（同 mongo `$in` 含 null）。
- **sqlite 的 `Ne(f, v)`（v 非 nil）生成 `(expr IS NULL OR expr <> ?)` 的 OR 形式，无法命中 `json_extract` 表达式索引**，大表上该条件会退化为全表扫描；需要走索引的"排除某值"查询请改用区间条件（如 `Lt`/`Gt`）或走逃生舱。
- `Limit` 在 `List` 和 `ForEach` 中生效，`Count` 忽略；`Limit`/`Skip` 传负值按 0 处理。
- sqlite 的字段名/表名/索引名只允许 `[A-Za-z0-9_.]`（索引名额外允许 `-`，用于自动生成的降序后缀）；非法名称返回错误，不会拼进 SQL。
- `EnsureIndexes` 遇到空 `Key` 返回错误（不 panic）。
- 复合 `Sparse` 索引的过滤条件两端一致：至少一个键存在即建索引条目（OR），全部缺失则不收录。
- 复合 `Sparse+Unique` 的部分键缺失场景两端有差异：mongo 的复合 sparse 唯一索引会对"仅 a 相同、b 缺失"的文档判重（E11000），sqlite 的唯一索引把 NULL 视为互不相同，因此不判重；全键都存在的组合两端一致。
- 单字段显式 JSON null 与"字段缺失"在 sqlite 无法区分（`json_extract` 两者都返回 NULL），而 mongo 的 sparse 索引仍会收录显式 null；需要区分时请避免存 `null`，或走逃生舱。
- `kv/redis` 的空字符串 key 返回错误（不 panic）；`Options.Prefix` 不同的 Store 互不可见。

## Test

```bash
docker compose up -d   # redis(6380) + mongo(27018)，见 docker-compose.yml
go test ./...          # 服务不可用时 redis/mongo 集成测试自动跳过
```

端口被占用时可设置 `LIBDB_TEST_REDIS_ADDR` / `LIBDB_TEST_MONGO_URI` 覆盖。
