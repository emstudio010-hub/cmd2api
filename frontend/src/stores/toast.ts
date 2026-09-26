import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ToastTone = 'success' | 'error' | 'info' | 'warning'

export interface ToastItem {
  id: number
  tone: ToastTone
  title: string
  description: string
  /** 毫秒；0 表示不自动消失（错误详情这种需要人读的用得上）。 */
  duration: number
}

let nextId = 1
/** 定时器放在响应式状态之外：它不该被序列化，也不该触发重渲染。 */
const timers = new Map<number, ReturnType<typeof setTimeout>>()

export const useToastStore = defineStore('toast', () => {
  const items = ref<ToastItem[]>([])

  function dismiss(id: number): void {
    const timer = timers.get(id)
    if (timer) {
      clearTimeout(timer)
      timers.delete(id)
    }
    items.value = items.value.filter((item) => item.id !== id)
  }

  function push(tone: ToastTone, title: string, description = '', duration = 4000): number {
    const id = nextId++
    // 同屏最多留 4 条，超出的挤掉最老的——通知栏不该遮住半个屏幕。
    if (items.value.length >= 4) {
      dismiss(items.value[0].id)
    }
    items.value = [...items.value, { id, tone, title, description, duration }]
    if (duration > 0) {
      timers.set(
        id,
        setTimeout(() => dismiss(id), duration),
      )
    }
    return id
  }

  function success(title: string, description = ''): number {
    return push('success', title, description)
  }

  function error(title: string, description = '', duration = 6000): number {
    return push('error', title, description, duration)
  }

  function info(title: string, description = ''): number {
    return push('info', title, description)
  }

  function warning(title: string, description = ''): number {
    return push('warning', title, description, 5000)
  }

  function clear(): void {
    for (const timer of timers.values()) clearTimeout(timer)
    timers.clear()
    items.value = []
  }

  return { items, push, success, error, info, warning, dismiss, clear }
})
