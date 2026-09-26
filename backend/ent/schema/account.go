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

// Account 定义上游账号实体，即一个 Command Code 账号（一把 user_ 开头的密钥）。
//
// 凭证以 JSONB 存在 credentials 里，结构随 type 变化：
//   - apikey: {"api_key": "user_xxx"}
//
// extra 存该账号绑定的设备身份（fingerprint seed / device_id）。
// Command Code 按设备指纹做风控，同一把 key 必须始终上报同一台设备，
// 所以指纹要在账号创建时生成一次并持久化，不能每次请求随机生成。
//
// 相对 sub2api 砍掉：proxy 代理链路、parent/children 影子账号、
// quota_dimension、session_window 系列、load_factor、连带的模型映射字段。
type Account struct {
	ent.Schema
}

func (Account) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "accounts"},
	}
}

func (Account) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.String("notes").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),

		// platform 目前恒为 commandcode。留字段是为了将来扩展时不必改表。
		field.String("platform").
			MaxLen(50).
			NotEmpty().
			Default(domain.PlatformCommandCode),
		field.String("type").
			MaxLen(20).
			NotEmpty().
			Default(domain.AccountTypeAPIKey),

		field.JSON("credentials", map[string]any{}).
			Default(func() map[string]any { return map[string]any{} }).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.JSON("extra", map[string]any{}).
			Default(func() map[string]any { return map[string]any{} }).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),

		// concurrency 是该账号同时可承载的在途请求数，调度器据此做准入。
		field.Int("concurrency").
			Default(3),
		// priority 越小越优先被选中。
		field.Int("priority").
			Default(50),
		field.Float("rate_multiplier").
			SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}).
			Default(1.0),

		field.String("status").
			MaxLen(20).
			Default(domain.StatusActive),
		field.String("error_message").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),

		field.Time("last_used_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("expires_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),

		// ---- 调度状态机 ----
		// schedulable=false 表示暂时不参与分配（例如正在探测恢复）。
		field.Bool("schedulable").
			Default(true),
		field.Time("rate_limited_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("rate_limit_reset_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("overload_until").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),

		// ---- 上游余额快照 ----
		//
		// 这些是**缓存**，权威来源永远是上游。跟着探活一起刷新，列表页直接读这里，
		// 打开页面不会去打上游——一池几十个账号就是几十次上游请求，页面会卡死。
		//
		// 全部 Nillable：加列时表里已经有数据，非空列没有 SQL DEFAULT 是加不上去的。
		// 而且「没有值」本身就是有意义的状态（没探过活、或平台不支持查余额），
		// 用 0 当哨兵会跟「真的用完到 0」混在一起。
		//
		// 窗口明细拆成扁平列而不是塞一个 JSONB：这两种窗口是上游协议的一部分，
		// 结构固定，拆开之后在 psql 里能直接读、能排序筛选，不用解 JSON。
		field.Time("balance_fetched_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("balance_error").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		// balance_remaining 是当前计费周期的**剩余**额度（美元）。
		field.Float("balance_remaining").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.String("balance_plan_id").
			Optional().
			Nillable().
			MaxLen(64),
		// balance_period_end 是当前计费周期的结束时间，也就是月额度重置的时刻。
		field.Time("balance_period_end").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// 5 小时滚动窗口：订阅套餐的短期限流，跟月额度是两回事。
		field.Float("balance_5h_used").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("balance_5h_cap").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Time("balance_5h_reset_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// 周窗口：同样是订阅套餐的限流。
		field.Float("balance_weekly_used").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("balance_weekly_cap").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Time("balance_weekly_reset_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),

		// ---- 健康检查 ----
		// consecutive_failures 连续失败计数，达到阈值由健康检查任务自动禁用。
		field.Int("consecutive_failures").
			Default(0),
		field.Time("last_health_check_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Bool("last_health_check_ok").
			Default(false),
		field.String("last_health_check_error").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Int("latency_ms").
			Optional().
			Nillable(),
	}
}

func (Account) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("groups", Group.Type).
			Through("account_groups", AccountGroup.Type),
		edge.To("usage_logs", UsageLog.Type),
	}
}

func (Account) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("platform"),
		index.Fields("status"),
		index.Fields("schedulable"),
		index.Fields("last_used_at"),
		index.Fields("deleted_at"),
		// 调度热路径：先按平台筛可调度、再按优先级排序。
		index.Fields("platform", "priority"),
		index.Fields("priority", "status"),
	}
}
