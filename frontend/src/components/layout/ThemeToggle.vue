<script setup lang="ts">
import { useThemeStore } from '@/stores/theme'
import Icon from '@/components/ui/Icon.vue'

const theme = useThemeStore()
</script>

<template>
  <button
    type="button"
    class="flex h-8 w-8 items-center justify-center rounded-md border border-line bg-raised text-muted transition hover:border-line-strong hover:text-fg"
    :title="theme.isDark ? '切换到浅色主题' : '切换到深色主题'"
    :aria-label="theme.isDark ? '切换到浅色主题' : '切换到深色主题'"
    @click="theme.toggle()"
  >
    <Icon :name="theme.isDark ? 'sun' : 'moon'" :size="15" />
  </button>
</template>
