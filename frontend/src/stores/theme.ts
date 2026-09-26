import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { readStore, writeStore } from '@/utils/storage'

export type ThemeMode = 'dark' | 'light'

const THEME_KEY = 'cmd2api.theme'

function initialMode(): ThemeMode {
  return readStore(THEME_KEY) === 'light' ? 'light' : 'dark'
}

export const useThemeStore = defineStore('theme', () => {
  const mode = ref<ThemeMode>(initialMode())

  const isDark = computed(() => mode.value === 'dark')

  /**
   * 把当前模式写到 <html> 的类上。
   *
   * 两个类都显式设置：CSS 里深色写在 :root、浅色写在 html.light，
   * 只删不加会让「从 light 切回 dark」正常工作但反向失效。
   */
  function apply(): void {
    const root = document.documentElement
    root.classList.toggle('dark', mode.value === 'dark')
    root.classList.toggle('light', mode.value === 'light')
    writeStore(THEME_KEY, mode.value)
  }

  function setMode(next: ThemeMode): void {
    mode.value = next
    apply()
  }

  function toggle(): void {
    setMode(mode.value === 'dark' ? 'light' : 'dark')
  }

  /** 应用启动时对齐一次：index.html 里的内联脚本已经处理过首屏，这里补上。 */
  function init(): void {
    apply()
  }

  return { mode, isDark, setMode, toggle, init }
})
