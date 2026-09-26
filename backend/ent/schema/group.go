package schema

import (
	"cmd2api/ent/schema/mixins"
	"cmd2api/internal/domain"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Group 定义分组实体。
//
// 分组是「账号池」与「API Key」之间的中间层：一把 Key 属于一个分组，
// 分组圈定一批上游账号，请求按分组的倍率计费。
//
// 相对 sub2api 砍掉：订阅模式（subscription_type / 限额窗口）、
// 专属分组、模型白名单与定价表、rpm 限制、可见性开关。
type Group struct {
	ent.Schema
}

func (Group) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "groups"},
	}
}

func (Group) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (Group) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(100).
			NotEmpty().
			Unique(),
		field.String("description").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
		// platform 决定这个分组的账号池走哪种上游协议。
		//
		// 分组绑定平台而不是让账号池混装，是因为两种上游能提供的模型并不重合：
		// 混装会让「请求某个模型」被路由到根本没有该模型的账号上，报错还很难懂。
		// 一个分组只能装同平台账号，这个约束在绑定账号时校验。
		field.String("platform").
			MaxLen(50).
			NotEmpty().
			Default(domain.PlatformCommandCode),
		// rate_multiplier 是该分组对下游的计费倍率，与账号自身的倍率相乘。
		field.Float("rate_multiplier").
			SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}).
			Default(1.0),
		field.String("status").
			MaxLen(20).
			Default(domain.StatusActive),
	}
}

func (Group) Edges() []ent.Edge {
	return []ent.Edge{
		// 反向边必须用 From+Ref 指回 Account.groups，否则 ent 会把它当成
		// 第二条正向边并判定为 O2M，M2M 就建不起来。
		edge.From("accounts", Account.Type).
			Ref("groups").
			Through("account_groups", AccountGroup.Type),
		edge.To("api_keys", APIKey.Type),
		edge.To("usage_logs", UsageLog.Type),
	}
}

func (Group) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("platform"),
		index.Fields("deleted_at"),
	}
}
