<script setup lang="ts">
// 单文件图标集。整个后台只用一套线性图标，避免引入图标库带来的
// 体积和风格不一致问题；路径数据静态，v-html 是安全的。
import { computed } from 'vue'

import { iconPaths } from './icons'
import type { IconName } from './icons'

const props = withDefaults(
  defineProps<{
    name: IconName
    size?: number | string
    strokeWidth?: number | string
  }>(),
  { size: 16, strokeWidth: 1.75 },
)

const markup = computed(() => iconPaths[props.name] ?? '')
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    :stroke-width="strokeWidth"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
    class="shrink-0"
    v-html="markup"
  />
</template>
