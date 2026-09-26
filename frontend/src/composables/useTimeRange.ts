import { computed, ref } from 'vue'

import { fromLocalInputValue, toLocalInputValue } from '@/utils/format'

/** 后端硬上限：超过 90 天直接 400。前端提前拦住，省一次往返。 */
export const MAX_RANGE_DAYS = 90

export interface TimeRange {
  start: string
  end: string
}

const PRESET_OPTIONS = [
  { label: '最近 1 小时', value: '1' },
  { label: '最近 24 小时', value: '24' },
  { label: '最近 7 天', value: '168' },
  { label: '最近 30 天', value: '720' },
]

/**
 * 时间范围选择器（概览与日志两页共用）。
 *
 * 只负责「算出 RFC3339 的 start/end」和「自定义范围的合法性」，
 * 什么时候真的去请求由调用方决定——两页的触发时机不一样
 * （概览自动刷新，日志点查询）。
 */
export function useTimeRange(defaultHours = 24) {
  const mode = ref<'preset' | 'custom'>('preset')
  const preset = ref(String(defaultHours))
  const customStart = ref('')
  const customEnd = ref('')

  const error = computed(() => {
    if (mode.value !== 'custom') return ''
    if (!customStart.value || !customEnd.value) return '请选择完整的开始与结束时间'
    const start = new Date(customStart.value)
    const end = new Date(customEnd.value)
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) return '时间格式不正确'
    if (start.getTime() >= end.getTime()) return '开始时间必须早于结束时间'
    if (end.getTime() - start.getTime() > MAX_RANGE_DAYS * 86_400_000) {
      return `时间范围不能超过 ${MAX_RANGE_DAYS} 天`
    }
    return ''
  })

  const range = computed<TimeRange>(() => {
    if (mode.value === 'preset') {
      const end = new Date()
      const start = new Date(end.getTime() - Number(preset.value) * 3_600_000)
      return { start: start.toISOString(), end: end.toISOString() }
    }
    return {
      start: fromLocalInputValue(customStart.value),
      end: fromLocalInputValue(customEnd.value),
    }
  })

  /** 切到自定义模式，并以「最近 N 小时」预填，省得从空白开始选。 */
  function useCustom(hours = 24): void {
    const end = new Date()
    const start = new Date(end.getTime() - hours * 3_600_000)
    customStart.value = toLocalInputValue(start.toISOString())
    customEnd.value = toLocalInputValue(end.toISOString())
    mode.value = 'custom'
  }

  function usePreset(): void {
    mode.value = 'preset'
  }

  return {
    mode,
    preset,
    presetOptions: PRESET_OPTIONS,
    customStart,
    customEnd,
    error,
    range,
    useCustom,
    usePreset,
  }
}
