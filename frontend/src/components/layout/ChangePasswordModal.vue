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
const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'changed'): void
}>()

const auth = useAuthStore()
const toast = useToastStore()

const current = ref('')
const next = ref('')
const confirm = ref('')
const submitting = ref(false)
const serverError = ref('')

// 客户端校验只做后端也认的规则：后端是 `min=8`，这里保持一致，
// 避免出现「前端放行、后端 400」的体验断层。
const errors = computed(() => ({
  current: current.value ? '' : '请输入当前密码',
  next: !next.value ? '请输入新密码' : next.value.length < 8 ? '新密码至少 8 位' : '',
  confirm: !confirm.value ? '请再次输入新密码' : confirm.value !== next.value ? '两次输入不一致' : '',
}))

const valid = computed(() => !errors.value.current && !errors.value.next && !errors.value.confirm)

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      current.value = ''
      next.value = ''
      confirm.value = ''
      serverError.value = ''
    }
  },
)

function close(): void {
  emit('update:open', false)
}

async function submit(): Promise<void> {
  if (!valid.value || submitting.value) return
  submitting.value = true
  serverError.value = ''
  try {
    await auth.changePassword(current.value, next.value)
    toast.success('密码已更新', '下次登录请使用新密码')
    emit('changed')
    close()
  } catch (err) {
    serverError.value = toMessage(err, '修改密码失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal
    :open="open"
    title="修改密码"
    subtitle="修改后当前登录仍然有效，下次登录使用新密码"
    size="sm"
    @update:open="emit('update:open', $event)"
  >
    <form class="space-y-3.5" @submit.prevent="submit">
      <Field label="当前密码" required html-for="pwd-current">
        <Input
          id="pwd-current"
          v-model="current"
          type="password"
          autocomplete="current-password"
          placeholder="请输入当前登录密码"
        />
      </Field>

      <Field label="新密码" required html-for="pwd-next" hint="至少 8 位">
        <Input
          id="pwd-next"
          v-model="next"
          type="password"
          autocomplete="new-password"
          placeholder="请输入新密码"
        />
      </Field>

      <Field
        label="确认新密码"
        required
        html-for="pwd-confirm"
        :error="confirm ? errors.confirm : ''"
      >
        <Input
          id="pwd-confirm"
          v-model="confirm"
          type="password"
          autocomplete="new-password"
          placeholder="请再次输入新密码"
        />
      </Field>

      <p
        v-if="serverError"
        class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger/10 px-2.5 py-2 text-2xs text-danger"
      >
        <Icon name="alertCircle" :size="13" class="mt-px" />
        <span>{{ serverError }}</span>
      </p>
    </form>

    <template #footer>
      <Button variant="ghost" size="md" :disabled="submitting" @click="close">取消</Button>
      <Button
        variant="primary"
        size="md"
        :loading="submitting"
        :disabled="!valid"
        @click="submit"
      >
        保存
      </Button>
    </template>
  </Modal>
</template>
