<script setup lang="ts">
import Button from './Button.vue'
import Icon from './Icon.vue'
import Modal from './Modal.vue'

withDefaults(
  defineProps<{
    open: boolean
    title: string
    /** 支持用默认插槽塞更复杂的内容（例如错误详情）。 */
    message?: string
    confirmText?: string
    cancelText?: string
    tone?: 'default' | 'danger'
    loading?: boolean
  }>(),
  {
    message: '',
    confirmText: '确认',
    cancelText: '取消',
    tone: 'default',
    loading: false,
  },
)

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'confirm'): void
  (event: 'cancel'): void
}>()

function cancel(): void {
  emit('update:open', false)
  emit('cancel')
}
</script>

<template>
  <Modal
    :open="open"
    size="sm"
    :persistent="loading"
    @update:open="emit('update:open', $event)"
  >
    <template #header>
      <div class="flex items-start gap-3">
        <span
          class="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full"
          :class="tone === 'danger' ? 'bg-danger/12 text-danger' : 'bg-accent/12 text-accent'"
        >
          <Icon :name="tone === 'danger' ? 'alert' : 'info'" :size="15" />
        </span>
        <div class="min-w-0">
          <h2 class="text-sm font-semibold text-fg">{{ title }}</h2>
          <p v-if="message" class="mt-1 text-[13px] leading-relaxed text-muted">{{ message }}</p>
        </div>
      </div>
    </template>

    <div v-if="$slots.default" class="text-[13px] text-muted">
      <slot />
    </div>

    <template #footer>
      <Button variant="ghost" size="md" :disabled="loading" @click="cancel">
        {{ cancelText }}
      </Button>
      <Button
        :variant="tone === 'danger' ? 'danger' : 'primary'"
        size="md"
        :loading="loading"
        @click="emit('confirm')"
      >
        {{ confirmText }}
      </Button>
    </template>
  </Modal>
</template>
