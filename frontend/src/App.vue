<script setup lang="ts">
import ToastHost from '@/components/ui/ToastHost.vue'
</script>

<template>
  <!-- 根组件只负责渲染当前路由与全局提示层。应用骨架在 AppShell 里。 -->
  <RouterView />
  <ToastHost />
</template>
