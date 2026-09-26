<script setup lang="ts">
import { computed } from 'vue'

import Spinner from './Spinner.vue'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'subtle'
type Size = 'xs' | 'sm' | 'md'

const props = withDefaults(
  defineProps<{
    variant?: Variant
    size?: Size
    type?: 'button' | 'submit' | 'reset'
    loading?: boolean
    disabled?: boolean
    block?: boolean
    /** 方形图标按钮，用于表格行内操作。 */
    icon?: boolean
    title?: string
  }>(),
  { variant: 'secondary', size: 'sm', type: 'button', loading: false, disabled: false, block: false, icon: false },
)

const variants: Record<Variant, string> = {
  primary:
    'bg-accent text-accent-fg border border-transparent hover:bg-accent/90 active:bg-accent/80 shadow-panel',
  secondary:
    'bg-raised text-fg border border-line hover:border-line-strong hover:bg-overlay active:bg-raised',
  ghost: 'bg-transparent text-muted border border-transparent hover:bg-raised hover:text-fg',
  subtle: 'bg-accent/10 text-accent border border-transparent hover:bg-accent/15',
  danger:
    'bg-danger/10 text-danger border border-danger/30 hover:bg-danger/20 hover:border-danger/50 active:bg-danger/25',
}

const sizes: Record<Size, string> = {
  xs: 'h-6 px-2 text-2xs gap-1 rounded',
  sm: 'h-8 px-2.5 text-[13px] gap-1.5 rounded-md',
  md: 'h-9 px-3.5 text-sm gap-2 rounded-md',
}

const iconSizes: Record<Size, string> = {
  xs: 'h-6 w-6 rounded',
  sm: 'h-8 w-8 rounded-md',
  md: 'h-9 w-9 rounded-md',
}

const classes = computed(() => [
  'inline-flex items-center justify-center font-medium transition select-none',
  'disabled:cursor-not-allowed disabled:opacity-45',
  props.icon ? iconSizes[props.size] : sizes[props.size],
  variants[props.variant],
  props.block ? 'w-full' : '',
])
</script>

<template>
  <button
    :type="type"
    :class="classes"
    :disabled="disabled || loading"
    :title="title"
    :aria-busy="loading || undefined"
  >
    <Spinner v-if="loading" :size="size === 'md' ? 14 : 12" />
    <slot v-else name="leading" />
    <slot />
    <slot name="trailing" />
  </button>
</template>
