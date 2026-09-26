<script setup lang="ts">
import { computed, ref } from 'vue'

import { useElementSize } from '@/composables/useElementSize'
import type { ChartPoint } from './types'

// 手写的面积图。需求只有「一条时间序列」，为它引入图表库（哪怕很小）
// 也要多几百 KB 和一套自己的主题系统，不划算。
const props = withDefaults(
  defineProps<{
    points: ChartPoint[]
    height?: number
    tone?: 'accent' | 'success' | 'warning' | 'info'
    /** 纵轴与悬停提示里的数值格式化。 */
    formatValue?: (value: number) => string
    yTicks?: number
    emptyText?: string
  }>(),
  {
    height: 220,
    tone: 'accent',
    formatValue: (value: number) => String(Math.round(value)),
    yTicks: 4,
    emptyText: '当前时间范围内没有数据',
  },
)

const { target, width } = useElementSize()

const padding = { top: 12, right: 14, bottom: 22, left: 52 }

const toneClass: Record<string, string> = {
  accent: 'text-accent',
  success: 'text-success',
  warning: 'text-warning',
  info: 'text-info',
}

// 每个实例一个渐变 id：同一页面上有两张图时 id 撞了会串色。
const gradientId = `area-grad-${Math.random().toString(36).slice(2, 9)}`

const hoverIndex = ref<number | null>(null)

const hasData = computed(() => props.points.length > 0)
const plotWidth = computed(() => Math.max(width.value - padding.left - padding.right, 10))
const plotHeight = computed(() => Math.max(props.height - padding.top - padding.bottom, 10))

/** 取一个「整」的纵轴上界，避免出现 4837 这种刻度。 */
function niceMax(value: number): number {
  if (!Number.isFinite(value) || value <= 0) return 1
  const exponent = Math.floor(Math.log10(value))
  const base = Math.pow(10, exponent)
  const normalized = value / base
  const step = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 2.5 ? 2.5 : normalized <= 5 ? 5 : 10
  return step * base
}

const maxValue = computed(() => niceMax(Math.max(...props.points.map((point) => point.value), 0)))

function xAt(index: number): number {
  const count = props.points.length
  if (count <= 1) return padding.left + plotWidth.value / 2
  return padding.left + (index / (count - 1)) * plotWidth.value
}

function yAt(value: number): number {
  const ratio = maxValue.value === 0 ? 0 : value / maxValue.value
  return padding.top + plotHeight.value - ratio * plotHeight.value
}

const linePath = computed(() => {
  if (!hasData.value || width.value === 0) return ''
  return props.points
    .map((point, index) => `${index === 0 ? 'M' : 'L'}${xAt(index).toFixed(2)} ${yAt(point.value).toFixed(2)}`)
    .join(' ')
})

const areaPath = computed(() => {
  if (!linePath.value) return ''
  const baseline = (padding.top + plotHeight.value).toFixed(2)
  const first = xAt(0).toFixed(2)
  const last = xAt(props.points.length - 1).toFixed(2)
  return `${linePath.value} L${last} ${baseline} L${first} ${baseline} Z`
})

const gridLines = computed(() => {
  const ticks = Math.max(props.yTicks, 2)
  return Array.from({ length: ticks + 1 }, (_, index) => {
    const ratio = index / ticks
    return {
      y: padding.top + plotHeight.value * ratio,
      label: props.formatValue(maxValue.value * (1 - ratio)),
    }
  })
})

/** X 轴只标几个刻度，密了反而读不出来。 */
const xLabels = computed(() => {
  const count = props.points.length
  if (count === 0) return []
  const desired = Math.max(2, Math.min(6, Math.floor(plotWidth.value / 90)))
  const step = Math.max(1, Math.ceil(count / desired))
  const out: { x: number; text: string }[] = []
  for (let index = 0; index < count; index += step) {
    out.push({ x: xAt(index), text: props.points[index].label })
  }
  const lastIndex = count - 1
  if (out[out.length - 1].x !== xAt(lastIndex)) {
    out.push({ x: xAt(lastIndex), text: props.points[lastIndex].label })
  }
  return out
})

// 在脚本里把悬停点算成坐标，模板里就不必用可能为 null 的下标去索引数组。
const marker = computed(() => {
  const index = hoverIndex.value
  if (index === null) return null
  const point = props.points[index]
  if (!point) return null
  return { x: xAt(index), y: yAt(point.value), point }
})

/** 悬停提示贴着左右边缘时会溢出容器，这里把它夹回可视范围。 */
const tooltipStyle = computed(() => {
  if (!marker.value) return {}
  const half = 70
  const left = Math.min(Math.max(marker.value.x, half), Math.max(width.value - half, half))
  return { left: `${left}px` }
})

function onPointerMove(event: PointerEvent): void {
  if (!hasData.value) return
  const rect = (event.currentTarget as SVGElement).getBoundingClientRect()
  const x = event.clientX - rect.left
  const count = props.points.length
  if (count <= 1) {
    hoverIndex.value = 0
    return
  }
  const ratio = (x - padding.left) / plotWidth.value
  const index = Math.round(ratio * (count - 1))
  hoverIndex.value = Math.min(Math.max(index, 0), count - 1)
}
</script>

<template>
  <div ref="target" class="relative w-full" :style="{ height: `${height}px` }">
    <div
      v-if="!hasData"
      class="flex h-full items-center justify-center text-2xs text-subtle"
    >
      {{ emptyText }}
    </div>

    <svg
      v-else-if="width > 0"
      :width="width"
      :height="height"
      :class="toneClass[tone]"
      @pointermove="onPointerMove"
      @pointerleave="hoverIndex = null"
    >
      <defs>
        <linearGradient :id="gradientId" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="currentColor" stop-opacity="0.26" />
          <stop offset="100%" stop-color="currentColor" stop-opacity="0" />
        </linearGradient>
      </defs>

      <!-- 网格与纵轴刻度 -->
      <g>
        <template v-for="(line, index) in gridLines" :key="index">
          <line
            :x1="padding.left"
            :x2="width - padding.right"
            :y1="line.y"
            :y2="line.y"
            class="text-line"
            stroke="currentColor"
            stroke-width="1"
            :stroke-dasharray="index === gridLines.length - 1 ? undefined : '3 4'"
          />
          <text
            :x="padding.left - 8"
            :y="line.y + 3.5"
            text-anchor="end"
            class="tnum fill-current text-subtle text-[10px]"
          >
            {{ line.label }}
          </text>
        </template>
      </g>

      <path :d="areaPath" :fill="`url(#${gradientId})`" />
      <path
        :d="linePath"
        fill="none"
        stroke="currentColor"
        stroke-width="1.75"
        stroke-linejoin="round"
        stroke-linecap="round"
      />

      <!-- 悬停指示 -->
      <g v-if="marker">
        <line
          :x1="marker.x"
          :x2="marker.x"
          :y1="padding.top"
          :y2="padding.top + plotHeight"
          class="text-line-strong"
          stroke="currentColor"
          stroke-width="1"
        />
        <circle
          :cx="marker.x"
          :cy="marker.y"
          r="3.5"
          fill="currentColor"
          class="text-accent"
          stroke="rgb(var(--c-surface))"
          stroke-width="2"
        />
      </g>

      <!-- 横轴刻度 -->
      <g>
        <text
          v-for="(label, index) in xLabels"
          :key="index"
          :x="label.x"
          :y="height - 6"
          text-anchor="middle"
          class="tnum fill-current text-subtle text-[10px]"
        >
          {{ label.text }}
        </text>
      </g>
    </svg>

    <div
      v-if="marker"
      class="pointer-events-none absolute top-1 z-10 -translate-x-1/2 whitespace-nowrap rounded-md border border-line bg-overlay px-2 py-1 shadow-pop"
      :style="tooltipStyle"
    >
      <p class="text-2xs text-subtle">{{ marker.point.tooltip || marker.point.label }}</p>
      <p class="tnum text-[12px] font-medium text-fg">
        {{ formatValue(marker.point.value) }}
      </p>
    </div>
  </div>
</template>
