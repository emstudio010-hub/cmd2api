<script setup lang="ts">
// 纯 CSS 转圈。比图标 + animate-spin 更省一层 DOM，且在任意尺寸下都清晰。
withDefaults(
  defineProps<{
    size?: number
    /** 深色底上的加载指示（例如主按钮里）用白色描边。 */
    invert?: boolean
  }>(),
  { size: 14, invert: false },
)
</script>

<template>
  <span
    class="inline-block animate-spin rounded-full border-2 border-current border-t-transparent align-[-0.125em]"
    :class="invert ? 'text-accent-fg' : ''"
    :style="{ width: `${size}px`, height: `${size}px` }"
    role="status"
    aria-label="加载中"
  />
</template>
