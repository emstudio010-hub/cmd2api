<script setup lang="ts">
import Icon from './Icon.vue'
import type { IconName } from './icons'

// 空态。后台里空列表是常态（刚部署、筛选太窄），所以文案必须告诉
// 用户「为什么是空的」和「下一步做什么」，而不是一句「暂无数据」。
withDefaults(
  defineProps<{
    icon?: IconName
    title: string
    description?: string
    /** 出错时的空态用红色图标区分。 */
    tone?: 'neutral' | 'danger' | 'warning'
  }>(),
  { icon: 'database', description: '', tone: 'neutral' },
)
</script>

<template>
  <div class="flex flex-col items-center justify-center gap-2 px-4 py-6 text-center">
    <span
      class="flex h-10 w-10 items-center justify-center rounded-full"
      :class="{
        'bg-raised text-subtle': tone === 'neutral',
        'bg-danger/12 text-danger': tone === 'danger',
        'bg-warning/12 text-warning': tone === 'warning',
      }"
    >
      <Icon :name="icon" :size="18" />
    </span>
    <p class="text-[13px] font-medium text-fg">{{ title }}</p>
    <p v-if="description" class="max-w-md text-2xs leading-relaxed text-subtle">
      {{ description }}
    </p>
    <div v-if="$slots.default" class="mt-2 flex flex-wrap items-center justify-center gap-2">
      <slot />
    </div>
  </div>
</template>
