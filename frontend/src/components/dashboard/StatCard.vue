<script setup lang="ts">
import Icon from '@/components/ui/Icon.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import type { IconName } from '@/components/ui/icons'

// 概览数字卡。数字用等宽体且字号明显大于标签——操作台里一眼扫到的
// 是数字，不是标签。
withDefaults(
  defineProps<{
    label: string
    value: string
    hint?: string
    icon?: IconName
    tone?: 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'info'
    loading?: boolean
  }>(),
  { hint: '', icon: 'activity', tone: 'neutral', loading: false },
)

const iconTones: Record<string, string> = {
  neutral: 'bg-raised text-muted',
  accent: 'bg-accent/12 text-accent',
  success: 'bg-success/12 text-success',
  warning: 'bg-warning/12 text-warning',
  danger: 'bg-danger/12 text-danger',
  info: 'bg-info/12 text-info',
}

const valueTones: Record<string, string> = {
  neutral: 'text-fg',
  accent: 'text-accent',
  success: 'text-success',
  warning: 'text-warning',
  danger: 'text-danger',
  info: 'text-info',
}
</script>

<template>
  <div class="card px-3 py-2.5">
    <div class="flex items-center gap-2">
      <span
        class="flex h-6 w-6 items-center justify-center rounded-md"
        :class="iconTones[tone]"
      >
        <Icon :name="icon" :size="13" />
      </span>
      <span class="truncate text-2xs text-subtle">{{ label }}</span>
    </div>

    <Skeleton v-if="loading" class="mt-2 h-6 w-20" />
    <p v-else class="tnum mt-1.5 text-xl font-semibold leading-7" :class="valueTones[tone]">
      {{ value }}
    </p>

    <!-- 空提示也占一行，卡片高度才不会因为有无 hint 而参差 -->
    <p class="mt-0.5 truncate text-2xs text-subtle">{{ hint || ' ' }}</p>
  </div>
</template>
