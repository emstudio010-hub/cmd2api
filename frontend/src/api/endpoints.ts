import { del, get, post, put } from './client'
import type {
  Account,
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
  batchImport(payload: BatchImportPayload) {
    return post<BatchImportResult>('/accounts/batch', payload)
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
