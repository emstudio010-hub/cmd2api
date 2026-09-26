<script setup lang="ts">
import { computed } from 'vue'

import Badge from './Badge.vue'

// 状态 → 语气 + 文案的映射集中在这里。
// 账号与密钥的状态取值相同但语义不同（账号的 active 是「正常」，
// 密钥的 active 是「启用」），所以文案允许调用方覆盖。
const props = withDefaults(
  defineProps<{
    status?: string | null
    /** 覆盖默认文案，例如账号用「正常」而不是「启用」。 */
    label?: string
    dot?: boolean
    /** 状态没有可直接映射的默认文案时使用。 */
    fallback?: string
  }>(),
  { status: '', label: '', dot: true, fallback: '未知' },
)

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'info'

const table: Record<string, { tone: Tone; label: string }> = {
  active: { tone: 'success', label: '启用' },
  enabled: { tone: 'success', label: '启用' },
  ok: { tone: 'success', label: '正常' },
  disabled: { tone: 'neutral', label: '已禁用' },
  inactive: { tone: 'neutral', label: '未启用' },
  error: { tone: 'danger', label: '异常' },
  expired: { tone: 'warning', label: '已过期' },
  warning: { tone: 'warning', label: '警告' },
}

const entry = computed(() => {
  const key = (props.status ?? '').toLowerCase()
  return table[key] ?? null
})

const tone = computed<Tone>(() => entry.value?.tone ?? 'neutral')
const text = computed(() => props.label || entry.value?.label || props.status || props.fallback)
</script>

<template>
  <Badge :tone="tone" :dot="dot">{{ text }}</Badge>
</template>
