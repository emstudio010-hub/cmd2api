<script setup lang="ts">
import { computed } from 'vue'

import type { BarItem } from './types'

// 横向条形排行。用纯 div 而不是 SVG：条形只需要一个百分比宽度，
// SVG 在这里只会多一层坐标系换算。
const props = withDefaults(
  defineProps<{
    items: BarItem[]
    tone?: 'accent' | 'success' | 'warning' | 'info'
    formatValue?: (value: number) => string
    emptyText?: string
    /** 固定分母。不传时按最大值归一，让最长的一条占满。 */
    max?: number
  }>(),
  {
    tone: 'accent',
    formatValue: (value: number) => String(value),
    emptyText: '暂无数据',
    max: 0,
  },
)

const toneClass: Record<string, string> = {
  accent: 'bg-accent/70',
  success: 'bg-success/70',
  warning: 'bg-warning/70',
  info: 'bg-info/70',
}

const denominator = computed(() => {
  if (props.max > 0) return props.max
  return Math.max(...props.items.map((item) => item.value), 0)
})

function barWidth(value: number): string {
  const total = denominator.value
  if (total <= 0) return '0%'
  // 最小 2%：值为 0 附近时留一丝可见的痕迹，否则看起来像渲染失败。
  const ratio = Math.min(Math.max(value / total, 0), 1)
  return `${Math.max(ratio * 100, value > 0 ? 2 : 0)}%`
}
</script>

<template>
  <div v-if="items.length === 0" class="py-6 text-center text-2xs text-subtle">
    {{ emptyText }}
  </div>

  <ul v-else class="space-y-2.5">
    <li v-for="item in items" :key="item.key">
      <div class="flex items-baseline justify-between gap-3">
        <span class="truncate font-mono text-[12px] text-fg" :title="item.label">
          {{ item.label }}
        </span>
        <span class="tnum shrink-0 text-[12px] text-muted">
          {{ formatValue(item.value) }}
          <span v-if="item.secondary" class="ml-1.5 text-2xs text-subtle">{{ item.secondary }}</span>
        </span>
      </div>
      <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-raised">
        <div
          class="h-full rounded-full transition-[width] duration-300"
          :class="toneClass[tone]"
          :style="{ width: barWidth(item.value) }"
        />
      </div>
    </li>
  </ul>
</template>
