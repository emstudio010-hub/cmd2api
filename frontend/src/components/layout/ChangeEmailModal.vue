<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Button from '@/components/ui/Button.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Modal from '@/components/ui/Modal.vue'
import { toMessage } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (event: 'update:open', value: boolean): void }>()

const auth = useAuthStore()
const toast = useToastStore()

const email = ref('')
const password = ref('')
const submitting = ref(false)
const serverError = ref('')

/**
 * 客户端校验只做后端也认的规则：非空 + 有个像样的 @。
 *
 * 后端用 net/mail 做的判断比这严得多（它会拒掉带显示名的
 * `Admin <a@b.com>`、带换行的注入形状等），这里**刻意不重复实现**那一套。
 * 前端这层的目的只是别让用户对着一个明显填错的输入去点保存，真正的判定
 * 以后端为准——判错了会显示后端返回的那句话。
 */
const errors = computed(() => ({
  email: !email.value.trim()
    ? '请输入新的登录用户名'
    : !email.value.includes('@')
      ? '登录用户名是邮箱，请填写完整地址'
      : '',
  password: password.value ? '' : '请输入当前密码以确认是你本人',
}))

const valid = computed(() => !errors.value.email && !errors.value.password)
const unchanged = computed(() => email.value.trim() === (auth.user?.email ?? ''))

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      // 预填当前用户名：多数人是想改个后缀或者修正拼写，不是从零写一个。
      email.value = auth.user?.email ?? ''
      password.value = ''
      serverError.value = ''
    }
  },
)

function close(): void {
  emit('update:open', false)
}

async function submit(): Promise<void> {
  if (!valid.value || submitting.value || unchanged.value) return
  submitting.value = true
  serverError.value = ''
  try {
    const user = await auth.changeEmail(password.value, email.value.trim())
    toast.success('登录用户名已更新', `下次登录请使用 ${user.email}`)
    close()
  } catch (err) {
    serverError.value = toMessage(err, '修改用户名失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal
    :open="open"
    title="修改用户名"
    subtitle="登录用户名就是邮箱。改完当前登录仍然有效，下次用新用户名登录"
    size="sm"
    @update:open="emit('update:open', $event)"
  >
    <form class="space-y-3.5" @submit.prevent="submit">
      <Field
        label="新用户名（邮箱）"
        required
        html-for="email-next"
        :error="email ? errors.email : ''"
      >
        <Input
          id="email-next"
          v-model="email"
          type="email"
          autocomplete="username"
          placeholder="you@example.com"
        />
      </Field>

      <Field
        label="当前密码"
        required
        html-for="email-password"
        hint="改登录入口是要紧操作，需要确认是你本人"
      >
        <Input
          id="email-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          placeholder="请输入当前登录密码"
        />
      </Field>

      <p
        v-if="serverError"
        class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger/10 px-2.5 py-2 text-2xs text-danger"
      >
        <Icon name="alertCircle" :size="13" class="mt-px" />
        <span>{{ serverError }}</span>
      </p>

      <p
        v-else-if="unchanged"
        class="rounded-md border border-line bg-raised px-2.5 py-2 text-2xs text-subtle"
      >
        这已经是当前的用户名了。改一个再保存。
      </p>
    </form>

    <template #footer>
      <Button variant="ghost" size="md" :disabled="submitting" @click="close">取消</Button>
      <Button
        variant="primary"
        size="md"
        :loading="submitting"
        :disabled="!valid || unchanged"
        @click="submit"
      >
        保存
      </Button>
    </template>
  </Modal>
</template>
