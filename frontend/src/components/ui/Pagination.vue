<script setup lang="ts">
import { computed } from 'vue'

import Icon from './Icon.vue'
import Select from './Select.vue'

const props = withDefaults(
  defineProps<{
    page: number
    pageSize: number
    total: number
    pageSizeOptions?: number[]
    /** 允许调整每页条数。关闭后只显示翻页按钮。 */
    showSizeChanger?: boolean
  }>(),
  { pageSizeOptions: () => [20, 50, 100, 200], showSizeChanger: true },
)

const emit = defineEmits<{
  (event: 'update:page', value: number): void
  (event: 'update:pageSize', value: number): void
}>()

const totalPages = computed(() => Math.max(1, Math.ceil(props.total / Math.max(props.pageSize, 1))))

const rangeStart = computed(() => (props.total === 0 ? 0 : (props.page - 1) * props.pageSize + 1))
const rangeEnd = computed(() => Math.min(props.page * props.pageSize, props.total))

const canPrev = computed(() => props.page > 1)
const canNext = computed(() => props.page < totalPages.value)

const sizeOptions = computed(() =>
  props.pageSizeOptions.map((size) => ({ label: `${size} 条/页`, value: String(size) })),
)

function go(target: number): void {
  const next = Math.min(Math.max(target, 1), totalPages.value)
  if (next !== props.page) emit('update:page', next)
}

function onSizeChange(value: string): void {
  const size = Number(value)
  if (!Number.isFinite(size) || size <= 0) return
  emit('update:pageSize', size)
  // 每页条数变了之后原来的页码大概率越界，直接退回第一页。
  emit('update:page', 1)
}
</script>

<template>
  <div
    class="flex flex-wrap items-center justify-between gap-3 border-t border-line px-3 py-2 text-2xs text-subtle"
  >
    <div class="tnum">
      共 <span class="font-medium text-muted">{{ total }}</span> 条
      <span v-if="total > 0">· 当前 {{ rangeStart }}–{{ rangeEnd }}</span>
    </div>

    <div class="flex items-center gap-2">
      <div v-if="showSizeChanger" class="w-[104px]">
        <Select
          size="sm"
          :model-value="String(pageSize)"
          :options="sizeOptions"
          @update:model-value="onSizeChange"
        />
      </div>

      <div class="flex items-center gap-1">
        <button
          type="button"
          class="flex h-6 w-6 items-center justify-center rounded border border-line text-muted transition hover:bg-raised hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="!canPrev"
          aria-label="上一页"
          @click="go(page - 1)"
        >
          <Icon name="chevronLeft" :size="13" />
        </button>
        <span class="tnum px-1 text-muted">{{ page }} / {{ totalPages }}</span>
        <button
          type="button"
          class="flex h-6 w-6 items-center justify-center rounded border border-line text-muted transition hover:bg-raised hover:text-fg disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="!canNext"
          aria-label="下一页"
          @click="go(page + 1)"
        >
          <Icon name="chevronRight" :size="13" />
        </button>
      </div>
    </div>
  </div>
</template>
