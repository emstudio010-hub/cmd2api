<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import Icon from '@/components/ui/Icon.vue'
import type { IconName } from '@/components/ui/icons'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (event: 'update:open', value: boolean): void }>()

const route = useRoute()

interface NavItem {
  name: string
  label: string
  icon: IconName
  hint: string
}

// 导航就是全部功能面：这个后台只有六个页面，不需要分组折叠。
const navItems: NavItem[] = [
  { name: 'dashboard', label: '概览', icon: 'dashboard', hint: '用量与运行状态' },
  { name: 'accounts', label: '上游账号', icon: 'layers', hint: 'Command Code 账号池' },
  { name: 'groups', label: '分组', icon: 'folder', hint: '账号与密钥的分组' },
  { name: 'keys', label: '下游密钥', icon: 'key', hint: '签发给客户端的 API Key' },
  { name: 'logs', label: '调用日志', icon: 'activity', hint: '逐条请求记录' },
  { name: 'settings', label: '运行配置', icon: 'settings', hint: '当前生效的参数' },
]

const activeName = computed(() => String(route.name ?? ''))

function close(): void {
  emit('update:open', false)
}
</script>

<template>
  <!-- 移动端遮罩。点击任意处关闭抽屉。 -->
  <Transition name="fade">
    <div
      v-if="open"
      class="fixed inset-0 z-30 bg-black/50 nav:hidden"
      aria-hidden="true"
      @click="close"
    />
  </Transition>

  <aside
    class="fixed inset-y-0 left-0 z-40 flex w-60 flex-col border-r border-line bg-surface transition-transform duration-200 nav:translate-x-0"
    :class="props.open ? 'translate-x-0' : '-translate-x-full'"
  >
    <!-- 品牌区 -->
    <div class="flex h-14 shrink-0 items-center gap-2.5 border-b border-line px-3.5">
      <span
        class="flex h-7 w-7 items-center justify-center rounded-md bg-accent text-accent-fg shadow-panel"
      >
        <Icon name="zap" :size="15" />
      </span>
      <div class="min-w-0 flex-1">
        <p class="truncate text-[13px] font-semibold leading-4 text-fg">cmd2api</p>
        <p class="truncate text-2xs leading-4 text-subtle">AI 网关管理后台</p>
      </div>
      <button
        type="button"
        class="-mr-1 rounded p-1 text-subtle transition hover:bg-raised hover:text-fg nav:hidden"
        aria-label="收起导航"
        @click="close"
      >
        <Icon name="close" :size="15" />
      </button>
    </div>

    <nav class="scroll-thin flex-1 overflow-y-auto px-2 py-3">
      <ul class="space-y-0.5">
        <li v-for="item in navItems" :key="item.name">
          <RouterLink
            :to="{ name: item.name }"
            class="group flex items-center gap-2.5 rounded-md px-2.5 py-2 text-[13px] transition"
            :class="
              activeName === item.name
                ? 'bg-accent/10 text-accent'
                : 'text-muted hover:bg-raised hover:text-fg'
            "
            @click="close"
          >
            <Icon :name="item.icon" :size="15" />
            <span class="flex-1 truncate font-medium">{{ item.label }}</span>
            <span
              v-if="activeName === item.name"
              class="h-1.5 w-1.5 rounded-full bg-accent"
              aria-hidden="true"
            />
          </RouterLink>
        </li>
      </ul>
    </nav>

    <div class="shrink-0 border-t border-line px-3.5 py-3">
      <div class="flex items-center gap-2 text-2xs text-subtle">
        <span class="flex h-1.5 w-1.5 rounded-full bg-success" aria-hidden="true" />
        <span>上游 · Command Code</span>
      </div>
      <p class="mt-1 text-2xs text-subtle">仅管理员模式 · 无注册入口</p>
    </div>
  </aside>
</template>
