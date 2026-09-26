<script setup lang="ts">
// 表单行：标签 + 控件 + 说明/错误。后台里几乎每个弹窗都是这个结构，
// 抽出来是为了让所有表单的间距和错误提示位置天然一致。
withDefaults(
  defineProps<{
    label?: string
    hint?: string
    error?: string
    required?: boolean
    htmlFor?: string
  }>(),
  { label: '', hint: '', error: '', required: false },
)
</script>

<template>
  <div class="space-y-1.5">
    <label
      v-if="label"
      :for="htmlFor"
      class="flex items-center gap-1 text-xs font-medium text-muted"
    >
      {{ label }}
      <span v-if="required" class="text-danger">*</span>
    </label>

    <slot />

    <p v-if="error" class="text-2xs text-danger">{{ error }}</p>
    <p v-else-if="hint" class="text-2xs text-subtle">{{ hint }}</p>
  </div>
</template>
