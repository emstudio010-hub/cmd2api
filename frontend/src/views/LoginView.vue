<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import Button from '@/components/ui/Button.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import ThemeToggle from '@/components/layout/ThemeToggle.vue'
import { toMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const toast = useToastStore()

const email = ref('')
const password = ref('')
const error = ref('')

const redirect = computed(() => {
  const raw = route.query.redirect
  return typeof raw === 'string' && raw.startsWith('/') ? raw : '/'
})

const canSubmit = computed(() => Boolean(email.value.trim()) && Boolean(password.value))

onMounted(() => {
  // 登录页唯一要做的事就是让人尽快输入，焦点直接给邮箱。
  const el = document.querySelector<HTMLInputElement>('#login-email')
  el?.focus()
})

async function submit(): Promise<void> {
  if (!canSubmit.value || auth.loggingIn) return
  error.value = ''
  try {
    const user = await auth.login(email.value, password.value)
    toast.success('登录成功', `欢迎回来，${user.email}`)
    await router.replace(redirect.value)
  } catch (err) {
    error.value = toMessage(err, '登录失败，请检查邮箱和密码')
    password.value = ''
  }
}
</script>

<template>
  <div class="relative flex min-h-screen flex-col items-center justify-center bg-canvas px-4 py-10">
    <div class="absolute right-4 top-4">
      <ThemeToggle />
    </div>

    <div class="w-full max-w-[22rem]">
      <div class="mb-6 flex flex-col items-center text-center">
        <img
          src="/logo.png"
          alt=""
          width="44"
          height="44"
          class="mb-3 h-11 w-11 rounded-xl object-cover shadow-panel"
        />
        <h1 class="text-lg font-semibold text-fg">cmd2api</h1>
        <p class="mt-1 text-2xs text-subtle">
          AI 网关管理后台 · 将 Command Code 账号转为 OpenAI / Anthropic 兼容接口
        </p>
      </div>

      <form class="card space-y-3.5 p-5" @submit.prevent="submit">
        <Field label="邮箱" required html-for="login-email">
          <Input
            id="login-email"
            v-model="email"
            type="email"
            autocomplete="username"
            placeholder="admin@example.com"
            :disabled="auth.loggingIn"
          />
        </Field>

        <Field label="密码" required html-for="login-password">
          <Input
            id="login-password"
            v-model="password"
            type="password"
            autocomplete="current-password"
            placeholder="请输入密码"
            :disabled="auth.loggingIn"
          />
        </Field>

        <p
          v-if="error"
          class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger/10 px-2.5 py-2 text-2xs leading-relaxed text-danger"
        >
          <Icon name="alertCircle" :size="13" class="mt-px" />
          <span>{{ error }}</span>
        </p>

        <Button
          type="submit"
          variant="primary"
          size="md"
          block
          :loading="auth.loggingIn"
          :disabled="!canSubmit"
        >
          登录
        </Button>
      </form>

      <p class="mt-4 text-center text-2xs leading-relaxed text-subtle">
        本系统仅管理员使用，不提供注册入口。<br />
        账号由部署时的管理员初始化配置创建。
      </p>
    </div>
  </div>
</template>
