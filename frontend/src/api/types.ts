// 与后端 DTO 一一对应的类型定义。
// 字段名保持与后端 JSON 完全一致（snake_case），中间不做转换——
// 转换层只会带来「哪个字段没了」的额外排查成本。

/** 账号状态。与 internal/domain 的常量一致。 */
export type AccountStatus = 'active' | 'disabled' | 'error'

/** 上游平台。后端只认识这两个值。 */
export type UpstreamPlatform = 'commandcode' | 'opencode'

/**
 * OpenCode 的计费模式：zen 按量付费、go 订阅额度。
 * 只有 OpenCode 平台有值，Command Code 账号这里是空串。
 */
export type AccountMode = 'zen' | 'go'

/** 分组 / 密钥状态。 */
export type CommonStatus = 'active' | 'disabled'

export interface AdminUser {
  id: number
  email: string
  role: string
  status: string
  last_login_at: string | null
  created_at: string
}

export interface LoginResponse {
  token: string
  expires_at: string
  user: AdminUser
}

export interface MeResponse {
  user: AdminUser
}

/** 分页请求的公共参数。 */
export interface PageQuery {
  page?: number
  page_size?: number
}

export interface DashboardSummary {
  requests: number
  failed: number
  /** 0~1 之间的小数，不是百分数。 */
  success_rate: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  total_cost: number
  avg_duration_ms: number
  avg_first_token_ms: number
}

export interface DashboardSeriesPoint {
  bucket: string
  requests: number
  input_tokens: number
  output_tokens: number
  total_cost: number
}

export interface DashboardBreakdown {
  key: string
  label: string
  requests: number
  tokens: number
  total_cost: number
}

export interface DashboardRuntime {
  accounts_total: number
  accounts_active: number
  api_keys_total: number
  bucket: 'hour' | 'day' | string
  /** 各平台的账号数，形如 {"commandcode": 3, "opencode": 1}。 */
  accounts_by_platform: Record<string, number>
}

export interface DashboardResponse {
  summary: DashboardSummary
  series: DashboardSeriesPoint[]
  models: DashboardBreakdown[]
  accounts: DashboardBreakdown[]
  runtime: DashboardRuntime
  start: string
  end: string
}

export interface Account {
  id: number
  name: string
  notes: string
  /** 上游平台。决定密钥校验规则与可绑定的分组。 */
  platform: UpstreamPlatform
  /** 计费模式（zen / go）。仅 OpenCode 有值，Command Code 是空串。 */
  account_mode: string
  /** 上游地址。仅 OpenCode 有值；留空时后端会回填该模式的默认地址。 */
  base_url: string
  type: string
  status: AccountStatus | string
  error_message: string | null
  /** 可能为空字符串——说明库里存的凭证解不开（换了加密密钥之类）。 */
  masked_key: string
  concurrency: number
  priority: number
  rate_multiplier: number
  schedulable: boolean
  consecutive_failures: number
  last_used_at: string | null
  expires_at: string | null
  last_health_check_at: string | null
  last_health_check_ok: boolean
  last_health_check_error: string | null
  latency_ms: number | null
  group_ids: number[]
  created_at: string
  updated_at: string
}

export interface AccountListResponse {
  items: Account[]
  total: number
  page: number
  page_size: number
}

export interface AccountQuery extends PageQuery {
  status?: string
  keyword?: string
  group_id?: number | null
  platform?: string
}

export interface CreateAccountPayload {
  name: string
  /** 必填。后端按它决定密钥校验规则，以及允许绑定哪些分组。 */
  platform: UpstreamPlatform
  /** 仅 OpenCode 需要（zen / go）。Command Code 传空串或不传。 */
  account_mode?: string
  /** 仅 OpenCode 使用。留空时后端按 account_mode 填默认地址。 */
  base_url?: string
  notes?: string
  api_key: string
  concurrency?: number
  priority?: number
  rate_multiplier?: number
  group_ids?: number[]
  expires_at?: string | null
}

/**
 * 更新时字段全部可选；expires_at 传空串表示清除过期时间。
 *
 * 注意这里**没有** platform：后端不接受改平台，传了也没用，
 * 干脆从类型上就断掉这条路。
 */
export interface UpdateAccountPayload {
  name?: string
  account_mode?: string
  base_url?: string
  notes?: string
  api_key?: string
  concurrency?: number
  priority?: number
  rate_multiplier?: number
  status?: string
  schedulable?: boolean
  group_ids?: number[]
  expires_at?: string
}

export interface AccountCheckResult {
  ok: boolean
  latency_ms: number
  error: string | null
}

export interface BatchImportPayload {
  keys: string
  /** 必填。整批共用同一个平台，OpenCode 的分组不能装 Command Code 账号。 */
  platform: UpstreamPlatform
  /** 仅 OpenCode 需要（zen / go）。 */
  account_mode?: string
  /** 仅 OpenCode 使用。留空时后端按 account_mode 填默认地址。 */
  base_url?: string
  group_ids?: number[]
  concurrency?: number
  priority?: number
}

export interface BatchImportFailure {
  line: number
  key: string
  error: string
}

export interface BatchImportResult {
  created: number
  created_ids: number[]
  failed: number
  failures: BatchImportFailure[]
}

export interface Group {
  id: number
  name: string
  description: string
  /** 分组所属平台。创建时定下，之后不可修改，只能装同平台的账号。 */
  platform: UpstreamPlatform
  rate_multiplier: number
  status: CommonStatus | string
  account_count: number
  api_key_count: number
  created_at: string
  updated_at: string
}

export interface GroupListResponse {
  items: Group[]
  total: number
}

export interface GroupPayload {
  name: string
  description?: string
  /** 创建时必填；更新时后端只接受与原值相同的平台。 */
  platform?: UpstreamPlatform
  rate_multiplier?: number
  status?: string
}

export interface ApiKey {
  id: number
  name: string
  /** 完整明文，管理员需要复制。 */
  key: string
  user_id: number
  group_id: number | null
  group_name: string
  status: CommonStatus | string
  /** 0 表示不限额。 */
  quota: number
  quota_used: number
  ip_whitelist: string[] | null
  last_used_at: string | null
  expires_at: string | null
  created_at: string
  updated_at: string
}

export interface ApiKeyListResponse {
  items: ApiKey[]
  total: number
}

export interface ApiKeyQuery {
  group_id?: number | null
  status?: string
  keyword?: string
}

export interface ApiKeyPayload {
  name?: string
  group_id?: number | null
  quota?: number
  ip_whitelist?: string[]
  expires_at?: string
  status?: string
}

export interface UsageLog {
  id: number
  model: string
  request_id: string
  user_id: number
  api_key_id: number
  api_key_name: string
  account_id: number
  account_name: string
  group_id: number | null
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  total_cost: number
  rate_multiplier: number
  stream: boolean
  duration_ms: number | null
  first_token_ms: number | null
  status_code: number
  error_message: string | null
  ip_address: string | null
  user_agent: string | null
  created_at: string
}

export interface UsageLogListResponse {
  items: UsageLog[]
  total: number
  page: number
  page_size: number
  start: string
  end: string
}

export type LogStatusFilter = '' | 'ok' | 'error'

export interface UsageLogQuery extends PageQuery {
  start?: string
  end?: string
  model?: string
  api_key_id?: number | null
  account_id?: number | null
  status?: LogStatusFilter
  request_id?: string
}

export interface ModelInfo {
  id: string
  name: string
}

export interface ModelListResponse {
  items: ModelInfo[]
  total?: number
  /** 没有分组/账号时后端会给一句提示，此时 items 是空数组而不是报错。 */
  hint?: string
}

export interface SettingsUpstream {
  base_url: string
  project_slug: string
  cli_version: string
  fingerprint_salt_set: boolean
  fingerprint_salt_masked: string
  empty_system_placeholder: boolean
  stream_idle_timeout: string
  nonstream_idle_timeout: string
  max_body_mb: number
  model_cache_ttl: string
}

export interface SettingsAuth {
  token_ttl: string
}

export interface SettingsHealthCheck {
  enabled: boolean
  interval: string
  failure_threshold: number
  timeout: string
}

export interface SettingsResponse {
  upstream: SettingsUpstream
  auth: SettingsAuth
  health_check: SettingsHealthCheck
}
