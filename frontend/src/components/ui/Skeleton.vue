<script setup lang="ts">
// 骨架屏。宽高一律由调用方的 class 决定，组件只负责底色的脉冲动画，
// 这样表格、卡片里的占位都能共用同一个组件。
withDefaults(defineProps<{ rounded?: string }>(), { rounded: 'rounded' })
</script>

<template>
  <span
    class="block animate-pulse bg-overlay"
    :class="rounded"
    aria-hidden="true"
  />
</template>
