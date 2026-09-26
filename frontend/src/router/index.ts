import { createRouter, createWebHistory } from 'vue-router'
import type { RouteRecordRaw } from 'vue-router'

import AppShell from '@/components/layout/AppShell.vue'
import { useAuthStore } from '@/stores/auth'

// 路由 meta 的类型补充：标题直接驱动顶栏，不再由每个页面自己渲染。
declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    subtitle?: string
    /** 免登录页面。目前只有登录页。 */
    public?: boolean
  }
}

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { title: '登录', public: true },
  },
  {
    path: '/',
    component: AppShell,
    children: [
      {
        path: '',
        name: 'dashboard',
        component: () => import('@/views/DashboardView.vue'),
        meta: { title: '概览', subtitle: '请求量、token 消耗与账号运行状态' },
      },
      {
        path: 'accounts',
        name: 'accounts',
        component: () => import('@/views/AccountsView.vue'),
        meta: { title: '上游账号', subtitle: 'Command Code / OpenCode 账号池与健康状态' },
      },
      {
        path: 'groups',
        name: 'groups',
        component: () => import('@/views/GroupsView.vue'),
        meta: { title: '分组', subtitle: '把账号池与下游密钥按用途分开' },
      },
      {
        path: 'keys',
        name: 'keys',
        component: () => import('@/views/KeysView.vue'),
        meta: { title: '下游密钥', subtitle: '签发给客户端使用的 API Key' },
      },
      {
        path: 'logs',
        name: 'logs',
        component: () => import('@/views/LogsView.vue'),
        meta: { title: '调用日志', subtitle: '逐条请求的用量、耗时与错误' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('@/views/SettingsView.vue'),
        meta: { title: '运行配置', subtitle: '当前生效的只读参数（由环境变量决定）' },
      },
    ],
  },
  // 未知路径回首页。后端 SPA fallback 会把 /anything 交给前端，
  // 没有这条兜底就会白屏。
  { path: '/:pathMatch(.*)*', redirect: { name: 'dashboard' } },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(_to, _from, savedPosition) {
    return savedPosition ?? { top: 0 }
  },
})

router.beforeEach((to) => {
  const auth = useAuthStore()

  if (to.meta.public) {
    // 已登录还去登录页就直接送回首页，避免出现「登出后又退回来」的错觉。
    if (auth.isAuthenticated && to.name === 'login') {
      return { name: 'dashboard' }
    }
    return true
  }

  if (!auth.isAuthenticated) {
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }

  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · cmd2api` : 'cmd2api 管理后台'
})

export default router
