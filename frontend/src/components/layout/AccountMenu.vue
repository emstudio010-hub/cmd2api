<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import Icon from '@/components/ui/Icon.vue'
import { useAuthStore } from '@/stores/auth'

const emit = defineEmits<{
  (event: 'change-password'): void
  (event: 'change-email'): void
  (event: 'logout'): void
}>()

const auth = useAuthStore()
const open = ref(false)
const root = ref<HTMLElement | null>(null)

const email = computed(() => auth.user?.email ?? '未登录')
const initial = computed(() => email.value.trim().charAt(0).toUpperCase() || '?')
const roleLabel = computed(() => (auth.user?.role === 'admin' ? '管理员' : (auth.user?.role ?? '')))

function toggle(): void {
  open.value = !open.value
}

function close(): void {
  open.value = false
}

function onDocumentPointerDown(event: PointerEvent): void {
  if (!open.value) return
  if (root.value && !root.value.contains(event.target as Node)) close()
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') close()
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocumentPointerDown)
  document.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  document.removeEventListener('keydown', onKeydown)
})

function pick(action: 'password' | 'email' | 'logout'): void {
  close()
  if (action === 'password') emit('change-password')
  else if (action === 'email') emit('change-email')
  else emit('logout')
}
</script>

<template>
  <div ref="root" class="relative">
    <button
      type="button"
      class="flex h-8 items-center gap-1.5 rounded-md border border-line bg-raised pl-1 pr-2 transition hover:border-line-strong"
      :aria-expanded="open"
      aria-haspopup="menu"
      @click="toggle"
    >
      <span
        class="flex h-6 w-6 items-center justify-center rounded bg-accent/15 text-2xs font-semibold text-accent"
      >
        {{ initial }}
      </span>
      <span class="hidden max-w-[10rem] truncate text-2xs text-muted sm:block">{{ email }}</span>
      <Icon name="chevronDown" :size="13" class="text-subtle" />
    </button>

    <Transition name="pop">
      <div
        v-if="open"
        class="absolute right-0 top-full z-50 mt-1.5 w-56 overflow-hidden rounded-panel border border-line bg-surface shadow-pop"
        role="menu"
      >
        <div class="border-b border-line px-3 py-2.5">
          <p class="truncate text-[13px] font-medium text-fg">{{ email }}</p>
          <p class="mt-0.5 text-2xs text-subtle">{{ roleLabel }} · 仅管理员模式</p>
        </div>

        <div class="p-1">
          <button
            type="button"
            class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] text-muted transition hover:bg-raised hover:text-fg"
            role="menuitem"
            @click="pick('password')"
          >
            <Icon name="lock" :size="14" />
            修改密码
          </button>
          <button
            type="button"
            class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] text-muted transition hover:bg-raised hover:text-fg"
            role="menuitem"
            @click="pick('email')"
          >
            <Icon name="users" :size="14" />
            修改用户名
          </button>
          <button
            type="button"
            class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] text-danger transition hover:bg-danger/10"
            role="menuitem"
            @click="pick('logout')"
          >
            <Icon name="logout" :size="14" />
            退出登录
          </button>
        </div>
      </div>
    </Transition>
  </div>
</template>
