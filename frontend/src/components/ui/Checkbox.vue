<script setup lang="ts">
import Icon from './Icon.vue'

withDefaults(
  defineProps<{
    disabled?: boolean
    label?: string
    description?: string
  }>(),
  { disabled: false, label: '', description: '' },
)

const model = defineModel<boolean>({ default: false })
</script>

<template>
  <label
    class="inline-flex items-start gap-2 text-[13px] text-fg"
    :class="disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'"
  >
    <span class="relative mt-px flex h-4 w-4 shrink-0">
      <input
        type="checkbox"
        class="peer h-4 w-4 cursor-pointer appearance-none rounded border border-line-strong bg-raised transition checked:border-accent checked:bg-accent focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-canvas disabled:cursor-not-allowed"
        :checked="model"
        :disabled="disabled"
        @change="model = ($event.target as HTMLInputElement).checked"
      />
      <Icon
        name="check"
        :size="11"
        :stroke-width="3"
        class="pointer-events-none absolute left-[3px] top-[3px] text-accent-fg opacity-0 transition peer-checked:opacity-100"
      />
    </span>
    <span v-if="label || description" class="leading-4">
      <span v-if="label" class="block">{{ label }}</span>
      <span v-if="description" class="mt-0.5 block text-2xs text-subtle">{{ description }}</span>
    </span>
  </label>
</template>
