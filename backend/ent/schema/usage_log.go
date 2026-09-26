package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UsageLog 记录每一次请求。只追加，不更新不删除。
//
// 相对 sub2api 砍掉：图片/视频计费字段、计费层级(billing_tier)、
// 模型映射链、订阅关联、缓存 TTL 覆盖、上游模型不一致标记。
// 保留 token 明细、成本、耗时、首字时间、状态码与错误信息——
// 这些是排障和统计真正会看的。
type UsageLog struct {
	ent.Schema
}

func (UsageLog) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "usage_logs"},
	}
}

func (UsageLog) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.Int64("api_key_id"),
		field.Int64("account_id"),
		field.Int64("group_id").
			Optional().
			Nillable(),

		field.String("request_id").
			MaxLen(64).
			NotEmpty(),
		// model 是客户端请求的模型名，原样记录，便于对账。
		field.String("model").
			MaxLen(100).
			NotEmpty(),
		// upstream_model 是真正发往 Command Code 的模型名（可能经过映射）。
		field.String("upstream_model").
			MaxLen(100).
			Optional().
			Nillable(),

		field.Int("input_tokens").
			Default(0),
		field.Int("output_tokens").
			Default(0),
		field.Int("cache_creation_tokens").
			Default(0),
		field.Int("cache_read_tokens").
			Default(0),

		field.Float("input_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.Float("output_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.Float("total_cost").
			Default(0).
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		// rate_multiplier 记录结算时用的综合倍率快照（分组倍率 × 账号倍率）。
		field.Float("rate_multiplier").
			Default(1).
			SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}),

		field.Bool("stream").
			Default(false),
		field.Int("duration_ms").
			Optional().
			Nillable(),
		field.Int("first_token_ms").
			Optional().
			Nillable(),
		field.Int("status_code").
			Default(200),
		field.String("error_message").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("user_agent").
			MaxLen(512).
			Optional().
			Nillable(),
		field.String("ip_address").
			MaxLen(45). // 容纳 IPv6
			Optional().
			Nillable(),

		field.Time("created_at").
			Default(time.Now).
			Immutable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (UsageLog) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("usage_logs").
			Field("user_id").
			Unique().
			Required(),
		edge.From("api_key", APIKey.Type).
			Ref("usage_logs").
			Field("api_key_id").
			Unique().
			Required(),
		edge.From("account", Account.Type).
			Ref("usage_logs").
			Field("account_id").
			Unique().
			Required(),
		edge.From("group", Group.Type).
			Ref("usage_logs").
			Field("group_id").
			Unique(),
	}
}

func (UsageLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"),
		index.Fields("model"),
		index.Fields("request_id"),
		index.Fields("account_id"),
		// 仪表盘按时间窗聚合时最常用的两条路径。
		index.Fields("user_id", "created_at"),
		index.Fields("api_key_id", "created_at"),
		index.Fields("account_id", "created_at"),
	}
}
