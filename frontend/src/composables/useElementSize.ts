import { onBeforeUnmount, onMounted, ref } from 'vue'
import type { Ref } from 'vue'

/**
 * 观测元素宽度。
 *
 * 图表必须按真实像素绘制：用 viewBox + preserveAspectRatio="none" 拉伸
 * 会让描边粗细和文字跟着变形，看起来就是「没做好」。
 */
export function useElementSize(): { target: Ref<HTMLElement | null>; width: Ref<number> } {
  const target = ref<HTMLElement | null>(null)
  const width = ref(0)
  let observer: ResizeObserver | null = null

  onMounted(() => {
    const node = target.value
    if (!node) return
    width.value = node.clientWidth
    if (typeof ResizeObserver === 'undefined') return
    observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        width.value = entry.contentRect.width
      }
    })
    observer.observe(node)
  })

  onBeforeUnmount(() => {
    observer?.disconnect()
    observer = null
  })

  return { target, width }
}
