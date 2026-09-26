// Package ent 提供生成的 ORM 代码。
package ent

// 启用 sql/upsert 以支持 ON CONFLICT（用量累加、账号状态条件更新）。
// 启用 sql/lock 以支持 FOR UPDATE 行锁（账号调度时防止并发选中同一账号）。
// --idtype int64 是刻意的选择：sub2api 全库用 int64 主键，沿用可以少一次类型转换，
// 也避免了 ent 默认 int 在 32 位平台上溢出的隐患。
// 启用 sql/execquery 以支持透传原生 SQL（仪表盘的时间序列聚合靠它）。
//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert,sql/lock,intercept,sql/execquery --idtype int64 ./schema
