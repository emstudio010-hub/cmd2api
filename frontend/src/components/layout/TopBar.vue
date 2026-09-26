<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import AccountMenu from './AccountMenu.vue'
import ThemeToggle from './ThemeToggle.vue'
import Icon from '@/components/ui/Icon.vue'

const emit = defineEmits<{
  (event: 'toggle-sidebar'): void
  (event: 'change-password'): void
  (event: 'change-email'): void
  (event: 'logout'): void
}>()

const route = useRoute()

const title = computed(() => route.meta.title ?? '')
const subtitle = computed(() => route.meta.subtitle ?? '')
</script>

<template>
  <header
    class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-line bg-canvas/85 px-3 backdrop-blur sm:px-5"
  >
    <button
      type="button"
      class="-ml-1 flex h-8 w-8 items-center justify-center rounded-md text-muted transition hover:bg-raised hover:text-fg nav:hidden"
      aria-label="展开导航"
      @click="emit('toggle-sidebar')"
    >
      <Icon name="menu" :size="17" />
    </button>

    <div class="min-w-0 flex-1">
      <h1 class="truncate text-[15px] font-semibold leading-5 text-fg">{{ title }}</h1>
      <p v-if="subtitle" class="hidden truncate text-2xs leading-4 text-subtle sm:block">
        {{ subtitle }}
      </p>
    </div>

    <div class="flex shrink-0 items-center gap-2">
      <ThemeToggle />
      <AccountMenu
        @change-password="emit('change-password')"
        @change-email="emit('change-email')"
        @logout="emit('logout')"
      />
    </div>
  </header>
</template>
