// sqlite 文档库最小示例：无需任何外部服务，直接 go run .
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/cupen/libdb"
	"github.com/cupen/libdb/errs"
	"github.com/cupen/libdb/orm"
	"github.com/cupen/libdb/orm/sqlite"
)

type User struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Age    int    `json:"age"`
	Status int    `json:"status"`
}

func (u User) GetID() any { return u.ID }

func (u User) DBIndex() []orm.Index {
	return []orm.Index{
		{Name: "idx_user_name", Key: []string{"name"}, Unique: true},
	}
}

func main() {
	ctx := context.Background()

	// 嵌入式文档库：文件库（换成 "file:memdb?mode=memory&cache=shared" 即内存库）
	db := libdb.Must(sqlite.Open("file:basic.db?_pragma=busy_timeout(5000)"))
	store := libdb.Must(sqlite.New[User](db, "user"))
	libdb.Must0(store.EnsureIndexes(ctx))

	// CRUD
	libdb.Must0(store.Create(ctx, User{ID: 1, Name: "alice", Age: 20, Status: 1}))
	libdb.Must0(store.Create(ctx, User{ID: 2, Name: "bob", Age: 30, Status: 1}))
	libdb.Must0(store.Save(ctx, User{ID: 3, Name: "carol", Age: 40, Status: 2})) // Save = upsert

	// 唯一索引：同名不同 ID → 冲突
	if err := store.Save(ctx, User{ID: 4, Name: "alice"}); errors.Is(err, errs.ErrAlreadyExists) {
		fmt.Println("唯一索引生效：", err)
	}

	alice, _ := store.Get(ctx, int64(1))
	fmt.Println("Get:", alice.Name, alice.Age)

	// Query DSL：状态为 1 且年龄大于 18，按年龄倒序
	users := libdb.Must(store.List(ctx, orm.Q().
		Eq("status", 1).
		Gt("age", 18).
		Sort("-age")))
	for _, u := range users {
		fmt.Println("List:", u.Name, u.Age)
	}

	n := libdb.Must(store.Count(ctx, orm.Q().Eq("status", 1)))
	fmt.Println("Count:", n)

	// 流式迭代：break 即提前终止
	for u, err := range store.ForEach(ctx, orm.Q().Sort("id")) {
		if err != nil {
			panic(err)
		}
		fmt.Println("ForEach:", u.Name)
		break
	}
}
