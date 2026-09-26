<script setup lang="ts">
// OpenCode 计费模式选择。
//
// 两种模式的默认上游地址不同，所以把「说明 + 默认地址」直接印在卡片上：
// 操作员选完就能看出 base_url 该填什么，不用去翻文档。
import { ACCOUNT_MODES } from '@/utils/platforms'

import Icon from './Icon.vue'

withDefaults(
  defineProps<{
    disabled?: boolean
    /** 校验失败时的红色描边。 */
    invalid?: boolean
  }>(),
  { disabled: false, invalid: false },
)

const model = defineModel<string>({ default: '' })
</script>

<template>
  <div class="grid gap-2 sm:grid-cols-2">
    <button
      v-for="item in ACCOUNT_MODES"
      :key="item.value"
      type="button"
      :disabled="disabled"
      :aria-pressed="model === item.value ? 'true' : 'false'"
      class="rounded-md border px-2.5 py-2 text-left transition"
      :class="[
        model === item.value
          ? 'border-accent bg-accent/10'
          : invalid
            ? 'border-danger bg-raised/40'
            : 'border-line bg-raised/40 hover:border-line-strong',
        disabled ? 'cursor-not-allowed opacity-70' : 'cursor-pointer',
      ]"
      @click="model = item.value"
    >
      <span class="flex items-center gap-1.5">
        <span
          class="h-1.5 w-1.5 shrink-0 rounded-full"
          :class="model === item.value ? 'bg-accent' : 'bg-subtle'"
          aria-hidden="true"
        />
        <span class="text-[13px] font-medium text-fg">{{ item.label }}</span>
        <Icon
          v-if="model === item.value"
          name="checkCircle"
          :size="13"
          class="ml-auto text-accent"
        />
      </span>
      <span class="mt-0.5 block text-2xs leading-4 text-subtle">{{ item.description }}</span>
      <span class="mt-1 block truncate font-mono text-2xs text-subtle">
        {{ item.defaultBaseUrl }}
      </span>
    </button>
  </div>
</template>
