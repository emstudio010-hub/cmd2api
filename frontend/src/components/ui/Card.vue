<script setup lang="ts">
// 面板容器。header / footer 用插槽，正文用默认插槽，
// padding 由调用方通过 bodyClass 决定（表格要贴着边，表单要留白）。
withDefaults(
  defineProps<{
    title?: string
    subtitle?: string
    bodyClass?: string
  }>(),
  { title: '', subtitle: '', bodyClass: 'p-4' },
)
</script>

<template>
  <section class="card overflow-hidden">
    <header
      v-if="title || $slots.header || $slots.actions"
      class="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-2.5"
    >
      <div class="min-w-0">
        <slot name="header">
          <h2 class="text-[13px] font-semibold text-fg">{{ title }}</h2>
          <p v-if="subtitle" class="mt-0.5 text-2xs text-subtle">{{ subtitle }}</p>
        </slot>
      </div>
      <div v-if="$slots.actions" class="flex shrink-0 items-center gap-2">
        <slot name="actions" />
      </div>
    </header>

    <div :class="bodyClass">
      <slot />
    </div>

    <footer v-if="$slots.footer" class="border-t border-line px-4 py-2.5">
      <slot name="footer" />
    </footer>
  </section>
</template>
