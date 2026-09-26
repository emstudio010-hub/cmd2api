<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Modal from '@/components/ui/Modal.vue'
import Select from '@/components/ui/Select.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { accountsApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { Account, CreateAccountPayload, Group, UpdateAccountPayload } from '@/api/types'
import { useToastStore } from '@/stores/toast'
import { fromLocalInputValue, toLocalInputValue } from '@/utils/format'

const props = defineProps<{
  open: boolean
  /** null 表示新建。 */
  account: Account | null
  groups: Group[]
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved'): void
}>()

const toast = useToastStore()

const isEdit = computed(() => props.account !== null)

const name = ref('')
const apiKey = ref('')
const notes = ref('')
const concurrency = ref('3')
const priority = ref('50')
const multiplier = ref('1')
const groupIds = ref<number[]>([])
const expiresAt = ref('')
/** 后端约定：expires_at 传空串表示清除过期时间。 */
const clearExpiry = ref(false)
const status = ref('active')
const schedulable = ref(true)

const submitting = ref(false)
const serverError = ref('')
const showKey = ref(false)

const statusOptions = [
  { label: '正常', value: 'active' },
  { label: '已禁用', value: 'disabled' },
  { label: '异常', value: 'error' },
]

function toNumber(value: string): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

const errors = computed(() => {
  const out: Record<string, string> = {}
  if (!name.value.trim()) out.name = '请输入账号名称'

  const key = apiKey.value.trim()
  if (!isEdit.value && !key) {
    out.apiKey = '请输入 Command Code 密钥'
  } else if (key && !key.startsWith('user_')) {
    out.apiKey = '密钥格式不正确：Command Code 密钥必须以 user_ 开头'
  }

  if (toNumber(concurrency.value) <= 0) out.concurrency = '并发数必须大于 0'
  if (toNumber(priority.value) < 0) out.priority = '优先级不能为负数'
  if (toNumber(multiplier.value) <= 0) out.multiplier = '倍率必须大于 0'
  return out
})

const valid = computed(() => Object.keys(errors.value).length === 0)

function reset(): void {
  const account = props.account
  name.value = account?.name ?? ''
  apiKey.value = ''
  notes.value = account?.notes ?? ''
  concurrency.value = String(account?.concurrency ?? 3)
  priority.value = String(account?.priority ?? 50)
  multiplier.value = String(account?.rate_multiplier ?? 1)
  groupIds.value = account ? [...account.group_ids] : []
  expiresAt.value = account?.expires_at ? toLocalInputValue(account.expires_at) : ''
  clearExpiry.value = false
  status.value = account?.status ?? 'active'
  schedulable.value = account?.schedulable ?? true
  serverError.value = ''
  showKey.value = false
}

watch(
  () => [props.open, props.account] as const,
  () => {
    if (props.open) reset()
  },
  { immediate: true },
)

function toggleGroup(id: number, checked: boolean): void {
  if (checked) {
    if (!groupIds.value.includes(id)) groupIds.value = [...groupIds.value, id]
  } else {
    groupIds.value = groupIds.value.filter((item) => item !== id)
  }
}

function onExpiryChange(value: string): void {
  expiresAt.value = value
  // 手动改了时间就不再是「清除」语义。
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
    const expires = clearExpiry.value ? '' : fromLocalInputValue(expiresAt.value) || undefined
    const key = apiKey.value.trim()

    if (isEdit.value && props.account) {
      const payload: UpdateAccountPayload = {
        name: name.value.trim(),
        notes: notes.value,
        concurrency: toNumber(concurrency.value),
        priority: toNumber(priority.value),
        rate_multiplier: toNumber(multiplier.value),
        status: status.value,
        schedulable: schedulable.value,
        group_ids: groupIds.value,
      }
      // 留空就是不换密钥——不然每次改个备注都会把凭证重写一遍。
      if (key) payload.api_key = key
      if (clearExpiry.value) payload.expires_at = ''
      else if (expires) payload.expires_at = expires

      await accountsApi.update(props.account.id, payload)
      toast.success('账号已更新', name.value.trim())
    } else {
      const payload: CreateAccountPayload = {
        name: name.value.trim(),
        notes: notes.value,
        api_key: key,
        concurrency: toNumber(concurrency.value),
        priority: toNumber(priority.value),
        rate_multiplier: toNumber(multiplier.value),
        group_ids: groupIds.value,
      }
      if (expires) payload.expires_at = expires

      await accountsApi.create(payload)
      toast.success('账号已创建', name.value.trim())
    }

    emit('saved')
    close()
  } catch (err) {
    serverError.value = toMessage(err, isEdit.value ? '更新账号失败' : '创建账号失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal
    :open="open"
    :title="isEdit ? '编辑账号' : '添加上游账号'"
    :subtitle="
      isEdit ? '留空的字段保持原值；密钥留空表示不修改' : '需要一把 Command Code 的 user_ 开头密钥'
    "
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
        <Field label="账号名称" required :error="errors.name">
          <Input v-model="name" placeholder="例如：主力账号 A" maxlength="100" />
        </Field>

        <Field
          label="API Key"
          :required="!isEdit"
          :error="errors.apiKey"
          :hint="isEdit ? '留空表示不修改当前密钥' : 'Command Code 密钥，必须以 user_ 开头'"
        >
          <div class="relative">
            <Input
              v-model="apiKey"
              :type="showKey ? 'text' : 'password'"
              :mono="true"
              :placeholder="isEdit ? '••••••••（留空不修改）' : 'user_xxxxxxxxxxxx'"
              autocomplete="off"            />
            <button
              type="button"
              class="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-1 text-subtle transition hover:text-fg"
              :aria-label="showKey ? '隐藏密钥' : '显示密钥'"
              @click="showKey = !showKey"
            >
              <Icon :name="showKey ? 'eyeOff' : 'eye'" :size="14" />
            </button>
          </div>
        </Field>
      </div>

      <Field label="备注" hint="给自己看的说明，例如用途或来源">
        <Textarea v-model="notes" :rows="2" placeholder="可选" />
      </Field>

      <div class="grid gap-3.5 sm:grid-cols-3">
        <Field
          label="并发数"
          :error="errors.concurrency"
          hint="该账号同时可承载的在途请求数"
        >
          <Input v-model="concurrency" type="number" min="1" step="1" />
        </Field>

        <Field
          label="优先级"
          :error="errors.priority"
          hint="数值越小越优先被调度"
        >
          <Input v-model="priority" type="number" min="0" step="1" />
        </Field>

        <Field
          label="费用倍率"
          :error="errors.multiplier"
          hint="1 表示按上游原价计费"
        >
          <Input v-model="multiplier" type="number" min="0" step="0.01" />
        </Field>
      </div>

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

      <div v-if="isEdit" class="grid gap-3.5 sm:grid-cols-2">
        <Field label="状态" hint="禁用后该账号不再参与调度">
          <Select v-model="status" :options="statusOptions" />
        </Field>
        <div class="flex items-end pb-1.5">
          <Checkbox
            v-model="schedulable"
            label="参与调度"
            description="关闭后保留账号但不分配请求"
          />
        </div>
      </div>

      <Field
        label="所属分组"
        hint="不选分组时该账号不会被任何下游密钥使用"
      >
        <div
          v-if="groups.length === 0"
          class="rounded-md border border-warning/30 bg-warning/10 px-2.5 py-2 text-2xs text-warning"
        >
          还没有任何分组。请先到「分组」页创建一个分组，否则该账号无法被下游调用。
        </div>
        <div v-else class="scroll-thin max-h-40 space-y-2 overflow-y-auto rounded-md border border-line bg-raised/40 p-2.5">
          <Checkbox
            v-for="group in groups"
            :key="group.id"
            :model-value="groupIds.includes(group.id)"
            :label="group.name"
            :description="`${group.account_count} 个账号 · ${group.api_key_count} 把密钥`"
            @update:model-value="toggleGroup(group.id, $event)"
          />
        </div>
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
        {{ isEdit ? '保存修改' : '创建账号' }}
      </Button>
    </template>
  </Modal>
</template>
