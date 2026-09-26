<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'

import AppSidebar from '@/components/layout/AppSidebar.vue'
import ChangeEmailModal from '@/components/layout/ChangeEmailModal.vue'
import ChangePasswordModal from '@/components/layout/ChangePasswordModal.vue'
import TopBar from '@/components/layout/TopBar.vue'
import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const toast = useToastStore()

const sidebarOpen = ref(false)
const passwordOpen = ref(false)
const emailOpen = ref(false)

// 路由变化时收起抽屉：移动端点完导航就该直接看到内容，
// 而不是还要再点一次遮罩。
watch(
  () => route.fullPath,
  () => {
    sidebarOpen.value = false
  },
)

function logout(): void {
  auth.clearSession()
  toast.info('已退出登录')
  router.replace({ name: 'login' })
}
</script>

<template>
  <div class="min-h-screen bg-canvas">
    <AppSidebar v-model:open="sidebarOpen" />

    <div class="flex min-h-screen flex-col nav:pl-60">
      <TopBar
        @toggle-sidebar="sidebarOpen = !sidebarOpen"
        @change-password="passwordOpen = true"
        @change-email="emailOpen = true"
        @logout="logout"
      />

      <main class="flex-1 px-3 py-4 sm:px-5 sm:py-5">
        <div class="mx-auto w-full max-w-[1500px]">
          <RouterView v-slot="{ Component }">
            <Transition name="fade" mode="out-in">
              <component :is="Component" />
            </Transition>
          </RouterView>
        </div>
      </main>
    </div>

    <ChangePasswordModal v-model:open="passwordOpen" />
    <ChangeEmailModal v-model:open="emailOpen" />
  </div>
</template>
