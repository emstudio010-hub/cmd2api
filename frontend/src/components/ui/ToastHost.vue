<script setup lang="ts">
import { storeToRefs } from 'pinia'

import Icon from './Icon.vue'
import { useToastStore } from '@/stores/toast'
import type { ToastTone } from '@/stores/toast'

const toast = useToastStore()
const { items } = storeToRefs(toast)

const styles: Record<ToastTone, { wrapper: string; icon: 'checkCircle' | 'alertCircle' | 'info' }> = {
  success: { wrapper: 'border-success/30 text-success', icon: 'checkCircle' },
  error: { wrapper: 'border-danger/35 text-danger', icon: 'alertCircle' },
  info: { wrapper: 'border-info/30 text-info', icon: 'info' },
  warning: { wrapper: 'border-warning/35 text-warning', icon: 'info' },
}
</script>

<template>
  <Teleport to="body">
    <div
      class="pointer-events-none fixed right-3 top-3 z-[60] flex w-[min(92vw,22rem)] flex-col gap-2"
      role="status"
      aria-live="polite"
    >
      <TransitionGroup
        enter-active-class="transition duration-150 ease-out"
        enter-from-class="translate-y-1 opacity-0"
        leave-active-class="transition duration-150 ease-in"
        leave-to-class="translate-x-2 opacity-0"
      >
        <div
          v-for="item in items"
          :key="item.id"
          class="pointer-events-auto flex items-start gap-2.5 rounded-panel border bg-surface px-3 py-2.5 shadow-pop"
          :class="styles[item.tone].wrapper"
        >
          <Icon :name="styles[item.tone].icon" :size="15" class="mt-px" />
          <div class="min-w-0 flex-1">
            <p class="text-[13px] font-medium text-fg">{{ item.title }}</p>
            <p v-if="item.description" class="mt-0.5 break-words text-2xs leading-relaxed text-muted">
              {{ item.description }}
            </p>
          </div>
          <button
            type="button"
            class="-mr-0.5 rounded p-0.5 text-subtle transition hover:bg-raised hover:text-fg"
            aria-label="关闭提示"
            @click="toast.dismiss(item.id)"
          >
            <Icon name="close" :size="13" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
