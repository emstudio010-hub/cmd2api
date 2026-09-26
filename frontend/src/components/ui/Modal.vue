<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'

import Icon from './Icon.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    subtitle?: string
    size?: 'sm' | 'md' | 'lg' | 'xl'
    closeOnBackdrop?: boolean
    /** 破坏性操作进行中时禁止随手关掉。 */
    persistent?: boolean
  }>(),
  {
    title: '',
    subtitle: '',
    size: 'md',
    closeOnBackdrop: true,
    persistent: false,
  },
)

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'close'): void
}>()

const panel = ref<HTMLElement | null>(null)

const widths = {
  sm: 'max-w-sm',
  md: 'max-w-lg',
  lg: 'max-w-2xl',
  xl: 'max-w-4xl',
} as const

const panelClass = computed(() => widths[props.size])

// 多个弹窗叠加时，body 的滚动锁要按引用计数，否则关掉上面一层
// 就会把下面那层的锁一起解掉。
let lockCount = 0
let previousOverflow = ''

function lockScroll(): void {
  if (lockCount === 0) {
    previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
  }
  lockCount += 1
}

function unlockScroll(): void {
  lockCount = Math.max(0, lockCount - 1)
  if (lockCount === 0) {
    document.body.style.overflow = previousOverflow
  }
}

function close(): void {
  if (props.persistent) return
  emit('update:open', false)
  emit('close')
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.stopPropagation()
    close()
  }
}

watch(
  () => props.open,
  async (isOpen) => {
    if (isOpen) {
      lockScroll()
      document.addEventListener('keydown', onKeydown)
      await nextTick()
      // 焦点移进弹窗：键盘用户不该还得先 Tab 一圈才进得来。
      const focusable = panel.value?.querySelector<HTMLElement>(
        'input:not([type="hidden"]), select, textarea, button, [href], [tabindex]:not([tabindex="-1"])',
      )
      ;(focusable ?? panel.value)?.focus()
    } else {
      document.removeEventListener('keydown', onKeydown)
      unlockScroll()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown)
  if (props.open) unlockScroll()
})
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div
        v-if="open"
        class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/60 p-4 pt-[8vh] backdrop-blur-[2px] sm:pt-[10vh]"
        role="presentation"
        @mousedown.self="closeOnBackdrop && close()"
      >
        <Transition name="pop" appear>
          <div
            ref="panel"
            class="relative w-full rounded-panel border border-line bg-surface shadow-pop outline-none"
            :class="panelClass"
            role="dialog"
            aria-modal="true"
            :aria-label="title || undefined"
            tabindex="-1"
          >
            <header
              v-if="title || $slots.header || !persistent"
              class="flex items-start justify-between gap-4 border-b border-line px-4 py-3"
            >
              <div class="min-w-0">
                <slot name="header">
                  <h2 class="truncate text-sm font-semibold text-fg">{{ title }}</h2>
                  <p v-if="subtitle" class="mt-0.5 text-2xs text-subtle">{{ subtitle }}</p>
                </slot>
              </div>
              <button
                v-if="!persistent"
                type="button"
                class="-mr-1 -mt-0.5 rounded p-1 text-subtle transition hover:bg-raised hover:text-fg"
                aria-label="关闭"
                @click="close"
              >
                <Icon name="close" :size="15" />
              </button>
            </header>

            <div class="px-4 py-4">
              <slot />
            </div>

            <footer
              v-if="$slots.footer"
              class="flex items-center justify-end gap-2 border-t border-line bg-raised/40 px-4 py-3"
            >
              <slot name="footer" />
            </footer>
          </div>
        </Transition>
      </div>
    </Transition>
  </Teleport>
</template>
