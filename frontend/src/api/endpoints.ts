import { del, get, post, put } from './client'
import type {
  Account,
  AccountBalanceRefreshResult,
  AccountCheckResult,
  AccountListResponse,
  AccountQuery,
  ApiKey,
  ApiKeyListResponse,
  ApiKeyPayload,
  ApiKeyQuery,
  BatchImportPayload,
  BatchImportResult,
  CreateAccountPayload,
  DashboardResponse,
  Group,
  GroupListResponse,
  GroupPayload,
  LoginResponse,
  MeResponse,
  ModelListResponse,
  SettingsResponse,
  AccountOAuthStatus,
  CompleteAccountOAuthPayload,
  EmailChangeResponse,
  StartAccountOAuthPayload,
  StartAccountOAuthResult,
  UpdateAccountPayload,
  UsageLogListResponse,
  UsageLogQuery,
} from './types'

/** 去掉 undefined / null / 空串的参数，避免后端把空串当成有效筛选值。 */
function clean(params: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    out[key] = value
  }
  return out
}

export const authApi = {
  login(email: string, password: string) {
    return post<LoginResponse>('/auth/login', { email, password })
  },
  me() {
    return get<MeResponse>('/auth/me')
  },
  changePassword(currentPassword: string, newPassword: string) {
    return post<{ message: string }>('/auth/password', {
      current_password: currentPassword,
      new_password: newPassword,
    })
  },
  /**
   * 改登录用户名（也就是邮箱）。
   *
   * 响应里带一把新令牌：邮箱写在 JWT 声明里，换名字就得换令牌，否则本地
   * 存的那把会一直带着旧邮箱。调用方要把它存回去。
   */
  changeEmail(currentPassword: string, email: string) {
    return put<EmailChangeResponse>('/auth/profile', {
      current_password: currentPassword,
      email,
    })
  },
}

export const dashboardApi = {
  fetch(start: string, end: string) {
    return get<DashboardResponse>('/dashboard', { params: clean({ start, end }) })
  },
}

export const accountsApi = {
  list(query: AccountQuery = {}) {
    return get<AccountListResponse>('/accounts', { params: clean({ ...query }) })
  },
  detail(id: number) {
    return get<Account>(`/accounts/${id}`)
  },
  create(payload: CreateAccountPayload) {
    return post<Account>('/accounts', payload)
  },
  update(id: number, payload: UpdateAccountPayload) {
    return put<Account>(`/accounts/${id}`, payload)
  },
  remove(id: number) {
    return del<{ message: string }>(`/accounts/${id}`)
  },
  /** 真会向上游发一次极小请求（消耗几十个 token），所以要给足超时。 */
  check(id: number) {
    return post<AccountCheckResult>(`/accounts/${id}/check`, undefined, { timeout: 120_000 })
  },
  /**
   * 只刷余额，不探活。
   *
   * 跟 check 分开是有意的：check 会真的发一次生成请求、消耗 token，
   * 这个只打两个只读接口，所以可以做成随便点的按钮。
   */
  refreshBalance(id: number) {
    return post<AccountBalanceRefreshResult>(`/accounts/${id}/balance`, undefined, {
      timeout: 60_000,
    })
  },
  batchImport(payload: BatchImportPayload) {
    return post<BatchImportResult>('/accounts/batch', payload)
  },
  /**
   * 发起浏览器授权，拿到要跳过去的地址。
   *
   * 这一步**不建账号**：账号要等 studio 把密钥带回回调地址才建得出来。
   */
  startOAuth(payload: StartAccountOAuthPayload) {
    return post<StartAccountOAuthResult>('/accounts/oauth/commandcode', payload)
  },
  /**
   * 问一句「这次授权好了没」。
   *
   * 授权是在另一个标签页里完成的，发起授权的这个页面靠轮询它知道结果，
   * 于是就不必整页跳走、表单也不会丢。
   */
  oauthStatus(state: string) {
    return get<AccountOAuthStatus>('/accounts/oauth/commandcode/status', { params: { state } })
  },
  /**
   * 手动收尾：用户把回调地址（或裸密钥）粘回来。
   *
   * 浏览器跳不回本机时的兜底。走这条路建出来的账号和自动那条完全一样。
   */
  completeOAuth(payload: CompleteAccountOAuthPayload) {
    return post<Account>('/accounts/oauth/commandcode/complete', payload)
  },
}

export const groupsApi = {
  list() {
    return get<GroupListResponse>('/groups')
  },
  create(payload: GroupPayload) {
    return post<Group>('/groups', payload)
  },
  update(id: number, payload: GroupPayload) {
    return put<Group>(`/groups/${id}`, payload)
  },
  remove(id: number) {
    return del<{ message: string }>(`/groups/${id}`)
  },
}

export const keysApi = {
  list(query: ApiKeyQuery = {}) {
    return get<ApiKeyListResponse>('/keys', { params: clean({ ...query }) })
  },
  create(payload: ApiKeyPayload) {
    return post<ApiKey>('/keys', payload)
  },
  update(id: number, payload: ApiKeyPayload) {
    return put<ApiKey>(`/keys/${id}`, payload)
  },
  remove(id: number) {
    return del<{ message: string }>(`/keys/${id}`)
  },
  resetQuota(id: number) {
    return post<{ message: string }>(`/keys/${id}/reset-quota`)
  },
}

export const logsApi = {
  list(query: UsageLogQuery = {}) {
    return get<UsageLogListResponse>('/logs', { params: clean({ ...query }) })
  },
}

export const metaApi = {
  models() {
    return get<ModelListResponse>('/models')
  },
  settings() {
    return get<SettingsResponse>('/settings')
  },
}

export * from './types'
