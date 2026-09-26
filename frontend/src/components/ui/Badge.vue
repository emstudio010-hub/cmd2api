<script setup lang="ts">
import { computed } from 'vue'

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'info'

const props = withDefaults(
  defineProps<{
    tone?: Tone
    dot?: boolean
    /** 用等宽字体显示（模型名、分组名这类技术标识）。 */
    mono?: boolean
  }>(),
  { tone: 'neutral', dot: false, mono: false },
)

const tones: Record<Tone, string> = {
  neutral: 'bg-overlay text-muted border-line',
  accent: 'bg-accent/12 text-accent border-accent/25',
  success: 'bg-success/12 text-success border-success/25',
  warning: 'bg-warning/12 text-warning border-warning/30',
  danger: 'bg-danger/12 text-danger border-danger/25',
  info: 'bg-info/12 text-info border-info/25',
}

const dotTones: Record<Tone, string> = {
  neutral: 'bg-subtle',
  accent: 'bg-accent',
  success: 'bg-success',
  warning: 'bg-warning',
  danger: 'bg-danger',
  info: 'bg-info',
}

const classes = computed(() => [
  'inline-flex items-center gap-1.5 rounded border px-1.5 py-px text-2xs font-medium leading-4 whitespace-nowrap',
  tones[props.tone],
  props.mono ? 'font-mono' : '',
])
</script>

<template>
  <span :class="classes">
    <span v-if="dot" class="h-1.5 w-1.5 shrink-0 rounded-full" :class="dotTones[tone]" />
    <slot />
  </span>
</template>
