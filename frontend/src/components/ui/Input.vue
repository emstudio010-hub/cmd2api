<script setup lang="ts">
import { computed, useAttrs } from 'vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    type?: string
    placeholder?: string
    disabled?: boolean
    readonly?: boolean
    size?: 'sm' | 'md'
    /** 校验失败时的红色描边。 */
    invalid?: boolean
    /** 等宽字体：密钥、IP、ID 这类字段用它更好读。 */
    mono?: boolean
    min?: number | string
    max?: number | string
    step?: number | string
    autocomplete?: string
    id?: string
    spellcheck?: boolean
  }>(),
  {
    type: 'text',
    placeholder: '',
    disabled: false,
    readonly: false,
    size: 'md',
    invalid: false,
    mono: false,
    spellcheck: false,
  },
)

// 双向绑定的值一律是字符串——这就是 DOM 里真实存的东西。
// type="number" 只影响浏览器的输入体验，数字转换交给提交时的
// Number()，省掉「空输入到底是 '' 还是 NaN」这类边界判断。
const model = defineModel<string>({ default: '' })

const attrs = useAttrs()

const classes = computed(() => [
  props.size === 'sm' ? 'field field-sm' : 'field',
  props.mono ? 'font-mono' : '',
  props.invalid ? 'border-danger focus:border-danger focus:ring-danger' : '',
  attrs.class,
])
</script>

<template>
  <input
    :id="id"
    :type="type"
    :value="model"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    :min="min"
    :max="max"
    :step="step"
    :autocomplete="autocomplete"
    :spellcheck="spellcheck"
    :class="classes"
    v-bind="{ ...attrs, class: undefined }"
    @input="model = ($event.target as HTMLInputElement).value"
  />
</template>
