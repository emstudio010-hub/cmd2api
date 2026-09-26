import axios from 'axios'
import type { AxiosError, AxiosInstance, AxiosRequestConfig } from 'axios'

import { readStore, removeStore, writeStore } from '@/utils/storage'

/** 令牌存放的键。auth store 与拦截器共用这一个常量。 */
export const TOKEN_KEY = 'cmd2api.token'

/** 后端统一的错误体。 */
interface ErrorBody {
  error?: string
  message?: string
}

/**
 * 所有请求失败后都会变成这个形状。
 *
 * 视图层只需要 `err.message` 就能直接弹 toast——不用再判断它到底是
 * axios 错误、网络错误还是业务错误。
 */
export class ApiError extends Error {
  readonly status: number
  readonly data: unknown

  constructor(message: string, status = 0, data: unknown = null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.data = data
  }
}

/** 视图层拿到的错误一定满足这个接口。 */
export interface ApiFailure {
  message: string
  status?: number
}

/** 401 时由 auth store 注册的回调：清空登录态并跳回登录页。 */
type UnauthorizedHandler = () => void

let unauthorizedHandler: UnauthorizedHandler | null = null

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  unauthorizedHandler = handler
}

export function getToken(): string | null {
  return readStore(TOKEN_KEY)
}

export function setToken(token: string): void {
  writeStore(TOKEN_KEY, token)
}

export function clearToken(): void {
  removeStore(TOKEN_KEY)
}

/** 把任意异常转成一句可以展示给用户的话。 */
export function toMessage(err: unknown, fallback = '请求失败，请稍后重试'): string {
  if (err instanceof ApiError) {
    return err.message || fallback
  }
  if (err instanceof Error) {
    return err.message || fallback
  }
  if (typeof err === 'string' && err) {
    return err
  }
  return fallback
}

/** 网络不通 / 超时这类没有 HTTP 响应的错误，文案要能指向真正的原因。 */
function transportMessage(error: AxiosError<ErrorBody>): string {
  if (error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT') {
    return '请求超时，请稍后重试'
  }
  if (error.code === 'ERR_CANCELED') {
    return '请求已取消'
  }
  if (error.message === 'Network Error' || !error.response) {
    return '无法连接服务器，请确认后端已启动'
  }
  return error.message || '请求失败'
}

/** 登录接口返回 401 表示「密码错了」，不能按令牌过期处理。 */
function isAuthFreeRequest(config: AxiosRequestConfig | undefined): boolean {
  const url = config?.url ?? ''
  return url.includes('/auth/login')
}

export const http: AxiosInstance = axios.create({
  baseURL: '/api',
  timeout: 30_000,
  headers: { 'Content-Type': 'application/json' },
})

http.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.set('Authorization', `Bearer ${token}`)
  }
  return config
})

http.interceptors.response.use(
  (response) => response,
  (error: AxiosError<ErrorBody>) => {
    const status = error.response?.status ?? 0
    const body = error.response?.data
    const serverMessage =
      (typeof body === 'object' && body !== null ? body.error || body.message : '') || ''

    if (status === 401 && !isAuthFreeRequest(error.config)) {
      // 令牌过期或失效：清掉本地令牌再交给上层跳登录页。
      clearToken()
      unauthorizedHandler?.()
    }

    const message = serverMessage || transportMessage(error)
    return Promise.reject(new ApiError(message, status, body ?? null))
  },
)

// ---- 薄封装：视图层不直接碰 axios 的泛型签名 ----

export async function get<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  const { data } = await http.get<T>(url, config)
  return data
}

export async function post<T>(
  url: string,
  body?: unknown,
  config?: AxiosRequestConfig,
): Promise<T> {
  const { data } = await http.post<T>(url, body, config)
  return data
}

export async function put<T>(url: string, body?: unknown, config?: AxiosRequestConfig): Promise<T> {
  const { data } = await http.put<T>(url, body, config)
  return data
}

export async function del<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  const { data } = await http.delete<T>(url, config)
  return data
}
