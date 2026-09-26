<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Button from '@/components/ui/Button.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Modal from '@/components/ui/Modal.vue'
import Select from '@/components/ui/Select.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { keysApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { ApiKey, ApiKeyPayload, Group } from '@/api/types'
import { useToastStore } from '@/stores/toast'
import { fromLocalInputValue, toLocalInputValue } from '@/utils/format'

const props = defineProps<{
  open: boolean
  /** null 表示新建。 */
  apiKey: ApiKey | null
  groups: Group[]
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved'): void
  /** 新建成功时把完整密钥交给父组件展示（列表里还要再点一次才看得到）。 */
  (event: 'created', value: ApiKey): void
}>()

const toast = useToastStore()

const isEdit = computed(() => props.apiKey !== null)

const name = ref('')
const groupId = ref('')
const quota = ref('0')
const ipWhitelist = ref('')
const expiresAt = ref('')
const clearExpiry = ref(false)
const status = ref('active')
const submitting = ref(false)
const serverError = ref('')

const statusOptions = [
  { label: '启用', value: 'active' },
  { label: '禁用', value: 'disabled' },
]

const groupOptions = computed(() => props.groups.map((g) => ({ label: g.name, value: String(g.id) })))

/** 每行一条，也接受逗号分隔——手写和粘贴都能用。 */
const whitelistEntries = computed(() =>
  ipWhitelist.value
    .split(/[\n,]/)
    .map((entry) => entry.trim())
    .filter(Boolean),
)

const errors = computed(() => {
  const out: Record<string, string> = {}
  if (!name.value.trim()) out.name = '请输入密钥名称'

  const parsed = Number(quota.value)
  if (!Number.isFinite(parsed) || parsed < 0) out.quota = '额度必须是不小于 0 的数字'

  if (!isEdit.value && !groupId.value && props.groups.length > 0) {
    out.groupId = '请选择分组'
  }

  const bad = whitelistEntries.value.find(
    (entry) => !/^[0-9a-fA-F:.]{3,45}(\/\d{1,3})?$/.test(entry),
  )
  if (bad) out.ip = `无法识别的地址：${bad}`
  return out
})

const valid = computed(() => Object.keys(errors.value).length === 0)

watch(
  () => [props.open, props.apiKey] as const,
  () => {
    if (!props.open) return
    const key = props.apiKey
    name.value = key?.name ?? ''
    groupId.value = key?.group_id ? String(key.group_id) : String(props.groups[0]?.id ?? '')
    quota.value = String(key?.quota ?? 0)
    ipWhitelist.value = (key?.ip_whitelist ?? []).join('\n')
    expiresAt.value = key?.expires_at ? toLocalInputValue(key.expires_at) : ''
    clearExpiry.value = false
    status.value = key?.status ?? 'active'
    serverError.value = ''
  },
  { immediate: true },
)

function onExpiryChange(value: string): void {
  expiresAt.value = value
  clearExpiry.value = false
}

function clearExpiryTime(): void {
  expiresAt.value = ''
  clearExpiry.value = true
}

function close(): void {
  emit('update:open', false)
}

async function submit(): Promise<void> {
  if (!valid.value || submitting.value) return
  submitting.value = true
  serverError.value = ''
  try {
    const payload: ApiKeyPayload = {
      name: name.value.trim(),
      quota: Number(quota.value),
      ip_whitelist: whitelistEntries.value,
      group_id: groupId.value ? Number(groupId.value) : null,
    }
    const expires = fromLocalInputValue(expiresAt.value)

    if (isEdit.value && props.apiKey) {
      payload.status = status.value
      if (clearExpiry.value) payload.expires_at = ''
      else if (expires) payload.expires_at = expires

      await keysApi.update(props.apiKey.id, payload)
      toast.success('密钥已更新', payload.name)
      emit('saved')
    } else {
      if (expires) payload.expires_at = expires
      const created = await keysApi.create(payload)
      toast.success('密钥已签发', created.name)
      emit('created', created)
      emit('saved')
    }
    close()
  } catch (err) {
    serverError.value = toMessage(err, isEdit.value ? '更新密钥失败' : '创建密钥失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal
    :open="open"
    :title="isEdit ? '编辑密钥' : '签发下游密钥'"
    :subtitle="isEdit ? '密钥本身不可更改，只能调整配置' : '创建后会完整展示一次密钥，请及时复制'"
    size="lg"
    @update:open="emit('update:open', $event)"
  >
    <form class="space-y-4" @submit.prevent="submit">
      <p
        v-if="serverError"
        class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger/10 px-2.5 py-2 text-2xs leading-relaxed text-danger"
      >
        <Icon name="alertCircle" :size="13" class="mt-px" />
        <span>{{ serverError }}</span>
      </p>

      <div class="grid gap-3.5 sm:grid-cols-2">
        <Field label="名称" required :error="errors.name">
          <Input v-model="name" placeholder="例如：内部工具 / 客户 A" maxlength="100" />
        </Field>

        <Field label="分组" :error="errors.groupId" hint="密钥只能使用同组内的上游账号">
          <Select
            v-model="groupId"
            :options="groupOptions"
            :placeholder="groups.length ? '请选择分组' : '暂无分组'"
            :disabled="groups.length === 0"
          />
        </Field>
      </div>

      <div class="grid gap-3.5 sm:grid-cols-2">
        <Field
          label="额度上限"
          :error="errors.quota"
          hint="0 表示不限额；已用额度可在列表里重置"
        >
          <Input v-model="quota" type="number" min="0" step="0.01" />
        </Field>

        <Field label="过期时间" hint="留空表示永不过期">
          <div class="flex items-center gap-2">
            <Input
              :model-value="expiresAt"
              type="datetime-local"
              :disabled="clearExpiry"
              @update:model-value="onExpiryChange"
            />
            <Button
              v-if="isEdit"
              variant="ghost"
              size="sm"
              type="button"
              :disabled="clearExpiry"
              @click="clearExpiryTime"
            >
              清除
            </Button>
          </div>
        </Field>
      </div>

      <Field
        label="IP 白名单"
        :error="errors.ip"
        hint="每行一个 IP 或 CIDR（也支持逗号分隔）；留空表示不限制来源"
      >
        <Textarea
          v-model="ipWhitelist"
          :rows="3"
          mono
          :placeholder="'203.0.113.10\n198.51.100.0/24'"
        />
      </Field>

      <Field v-if="isEdit" label="状态" hint="禁用后该密钥立即失效">
        <Select v-model="status" :options="statusOptions" />
      </Field>
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
        {{ isEdit ? '保存修改' : '签发密钥' }}
      </Button>
    </template>
  </Modal>
</template>
