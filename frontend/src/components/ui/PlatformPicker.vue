<script setup lang="ts">
// 上游平台选择卡片。
//
// 做成可点选的大卡片而不是下拉框：平台决定了表单后面所有字段的形态
// （密钥校验规则、是否有计费模式、能选哪些分组），是这个表单里第一个
// 也是最重要的决定，值得占满一行。
import { PLATFORMS } from '@/utils/platforms'

import Icon from './Icon.vue'

withDefaults(
  defineProps<{
    /** 只读展示。编辑态下平台由后端锁定，改不了。 */
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
      v-for="item in PLATFORMS"
      :key="item.value"
      type="button"
      :disabled="disabled"
      :aria-pressed="model === item.value ? 'true' : 'false'"
      class="rounded-md border px-3 py-2.5 text-left transition"
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
        <Icon v-else-if="disabled" name="lock" :size="12" class="ml-auto text-subtle" />
      </span>
      <span class="mt-0.5 block text-2xs leading-4 text-subtle">{{ item.description }}</span>
    </button>
  </div>
</template>
