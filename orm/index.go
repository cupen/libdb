package orm

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Index 是后端中立的索引描述。Key 支持 "-" 前缀表降序；
// ExpireAfter 仅 mongo 支持（TTL），sqlite 忽略。
type Index struct {
	Name        string // 留空时按 Key 自动生成
	Key         []string
	Unique      bool
	Sparse      bool
	ExpireAfter time.Duration
}

// GetName 返回索引名，为空时按 "字段_方向" 拼接生成。
func (i *Index) GetName() string {
	if i.Name == "" {
		nameArr := make([]string, 0, len(i.Key)*2)
		for _, key := range i.Key {
			name, ordering := parseField(key)
			nameArr = append(nameArr, name, strconv.Itoa(ordering))
		}
		i.Name = strings.Join(nameArr, "_")
	}
	return i.Name
}

func (i Index) String() string {
	return fmt.Sprintf("Index(%s) {Key: %v, Unique: %v, Sparse: %v, ExpireAfter: %v}",
		i.GetName(), i.Key, i.Unique, i.Sparse, i.ExpireAfter)
}

// parseField 解析 "-field" → ("field", -1)，"field" → ("field", 1)。
func parseField(field string) (key string, order int) {
	if strings.HasPrefix(field, "-") {
		return field[1:], -1
	}
	return field, 1
}
