import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { authApi } from '@/api/endpoints'
import { clearToken, getToken, setToken } from '@/api/client'
import type { AdminUser } from '@/api/types'

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(getToken())
  const user = ref<AdminUser | null>(null)
  /** bootstrap 是否跑过。路由守卫靠它决定要不要等。 */
  const ready = ref(false)
  const loggingIn = ref(false)

  const isAuthenticated = computed(() => Boolean(token.value))

  function applyToken(next: string | null): void {
    token.value = next
    if (next) {
      setToken(next)
    } else {
      clearToken()
    }
  }

  /** 清空本地登录态。不碰路由——跳转由调用方决定，避免 store 依赖 router。 */
  function clearSession(): void {
    applyToken(null)
    user.value = null
  }

  async function login(email: string, password: string): Promise<AdminUser> {
    loggingIn.value = true
    try {
      const result = await authApi.login(email.trim(), password)
      applyToken(result.token)
      user.value = result.user
      ready.value = true
      return result.user
    } finally {
      loggingIn.value = false
    }
  }

  async function fetchMe(): Promise<AdminUser> {
    const result = await authApi.me()
    user.value = result.user
    return result.user
  }

  /**
   * 启动时校验本地令牌还在不在有效期内。
   *
   * 返回 true 表示可以进入应用；false 表示令牌无效、已清掉，调用方应
   * 跳登录页。任何失败都按「未登录」处理——后端除了 401 也可能因为
   * 数据库没起来而报 500，那时让用户看到登录页比卡在空白页更好。
   */
  async function bootstrap(): Promise<boolean> {
    if (!token.value) {
      ready.value = true
      return false
    }
    try {
      await fetchMe()
      ready.value = true
      return true
    } catch {
      clearSession()
      ready.value = true
      return false
    }
  }

  async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
    await authApi.changePassword(currentPassword, newPassword)
  }

  return {
    token,
    user,
    ready,
    loggingIn,
    isAuthenticated,
    login,
    fetchMe,
    bootstrap,
    clearSession,
    changePassword,
  }
})
