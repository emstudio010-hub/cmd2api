package mixins

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// SoftDeleteMixin 只负责声明 deleted_at 列，不接管查询。
//
// 与 sub2api 的实现不同：这里刻意不用 ent 的 Interceptor/Hook 去改写查询——
// 那需要依赖生成出来的 ent/intercept 包，隐式改写所有语句，排障时很难看出
// 某条 SQL 到底带了什么条件。改成由 repository 层显式带上 deleted_at IS NULL，
// 行为一目了然。见 internal/repo 的 notDeleted 辅助函数。
type SoftDeleteMixin struct {
	mixin.Schema
}

func (SoftDeleteMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time("deleted_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}
