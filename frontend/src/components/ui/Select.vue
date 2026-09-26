<script setup lang="ts">
import { computed, useAttrs } from 'vue'

import type { SelectOption } from './types'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    options: SelectOption[]
    disabled?: boolean
    size?: 'sm' | 'md'
    invalid?: boolean
    /** 值为空时显示的首项文案。空串代表「不筛选」。 */
    placeholder?: string
    id?: string
  }>(),
  { disabled: false, size: 'md', invalid: false, placeholder: '' },
)

const model = defineModel<string>({ default: '' })

const attrs = useAttrs()
const classes = computed(() => [
  props.size === 'sm' ? 'field field-sm' : 'field',
  'cursor-pointer appearance-none bg-no-repeat pr-7',
  props.invalid ? 'border-danger' : '',
  attrs.class,
])
</script>

<template>
  <div class="relative inline-flex w-full">
    <select
      :id="id"
      :value="model"
      :disabled="disabled"
      :class="classes"
      v-bind="{ ...attrs, class: undefined }"
      @change="model = ($event.target as HTMLSelectElement).value"
    >
      <option v-if="placeholder" value="">{{ placeholder }}</option>
      <option
        v-for="option in options"
        :key="option.value"
        :value="option.value"
        :disabled="option.disabled"
      >
        {{ option.label }}
      </option>
    </select>
    <!-- 自绘箭头：原生 select 的箭头在深色主题下是浅色的，没法用。 -->
    <svg
      class="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-subtle"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M6 9l6 6 6-6" />
    </svg>
  </div>
</template>
