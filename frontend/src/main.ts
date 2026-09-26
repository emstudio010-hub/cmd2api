import { createPinia } from 'pinia'
import { createApp } from 'vue'

import App from './App.vue'
import router from './router'
import { setUnauthorizedHandler } from './api/client'
import { useAuthStore } from './stores/auth'
import { useThemeStore } from './stores/theme'
import './styles/main.css'

async function bootstrap(): Promise<void> {
  const app = createApp(App)

  // pinia 必须先装：下面的 store 调用依赖它。
  const pinia = createPinia()
  app.use(pinia)

  // 主题要在首帧之前落定，否则会闪一下默认配色。
  useThemeStore(pinia).init()

  // 令牌过期时由拦截器统一清登录态并跳登录页。
  // 这里才建立依赖：api 层不该 import 路由或 store（会形成循环）。
  const auth = useAuthStore(pinia)
  setUnauthorizedHandler(() => {
    auth.clearSession()
    const current = router.currentRoute.value
    if (current.name !== 'login') {
      router.replace({ name: 'login', query: { redirect: current.fullPath } })
    }
  })

  // 启动时校验一次本地令牌。失败就当作未登录，让路由守卫把用户送到登录页。
  await auth.bootstrap()

  app.use(router)
  await router.isReady()
  app.mount('#app')
}

void bootstrap()
