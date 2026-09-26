<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import AccountModePicker from '@/components/ui/AccountModePicker.vue'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Modal from '@/components/ui/Modal.vue'
import PlatformPicker from '@/components/ui/PlatformPicker.vue'
import Select from '@/components/ui/Select.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { accountsApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type {
  Account,
  CreateAccountPayload,
  Group,
  UpstreamPlatform,
  UpdateAccountPayload,
} from '@/api/types'
import { useToastStore } from '@/stores/toast'
import { fromLocalInputValue, toLocalInputValue } from '@/utils/format'
import { PLATFORMS, accountModeMeta, platformMeta } from '@/utils/platforms'

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

const subtitle = computed(() =>
  isEdit.value
    ? '留空的字段保持原值；密钥留空表示不修改；平台创建后不可更改'
    : '先选上游平台，再填写该平台的密钥',
)

/** 上游平台。新建时为空串（必须显式选）；编辑时是后端锁定好的既有值。 */
const platform = ref<UpstreamPlatform | ''>('')
/** 以下两个字段只有 OpenCode 用得到。 */
const accountMode = ref('')
const baseUrl = ref('')
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

const meta = computed(() => platformMeta(platform.value))
const usesMode = computed(() => meta.value.usesMode)
/** 当前模式留空 base_url 时会落到的地址，用作占位与提示。 */
const defaultBaseUrl = computed(() => accountModeMeta(accountMode.value).defaultBaseUrl)

/** 只列同平台的分组：跨平台的组合后端会直接 400。 */
const platformGroups = computed(() =>
  platform.value ? props.groups.filter((group) => group.platform === platform.value) : [],
)
const hiddenGroupCount = computed(() => props.groups.length - platformGroups.value.length)

const errors = computed(() => {
  const out: Record<string, string> = {}

  // 平台是第一个必选项，后面所有字段的形态都由它决定。
  if (!platform.value) out.platform = '请选择上游平台'
  if (!name.value.trim()) out.name = '请输入账号名称'

  const key = apiKey.value.trim()
  if (!isEdit.value && !key) {
    out.apiKey = `请输入 ${meta.value.label} 密钥`
  } else if (key && meta.value.keyPrefix && !key.startsWith(meta.value.keyPrefix)) {
    // OpenCode 的 keyPrefix 是空串，这里天然跳过前缀校验。
    out.apiKey = `密钥格式不正确：${meta.value.label} 密钥必须以 ${meta.value.keyPrefix} 开头`
  }

  if (usesMode.value && !accountMode.value) out.accountMode = '请选择计费模式'

  if (toNumber(concurrency.value) <= 0) out.concurrency = '并发数必须大于 0'
  if (toNumber(priority.value) < 0) out.priority = '优先级不能为负数'
  if (toNumber(multiplier.value) <= 0) out.multiplier = '倍率必须大于 0'
  return out
})

const valid = computed(() => Object.keys(errors.value).length === 0)

function reset(): void {
  const account = props.account
  // 编辑时平台不可改。platform 是后端后加的字段，历史数据里可能是空串，
  // 那按老平台的语义当作 Command Code——否则卡片是只读的，表单会永远卡在
  // 「请选择上游平台」上，连改个备注都做不到。
  platform.value = account ? account.platform || 'commandcode' : ''
  accountMode.value = account?.account_mode ?? ''
  baseUrl.value = account?.base_url ?? ''
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

/**
 * 切换平台。新建时才会走到这里（编辑态卡片是只读的）。
 * OpenCode 专属字段和已选分组在换平台后都不再适用，直接清空——
 * 留着就会拼出一个后端必然拒绝的跨平台组合。
 */
function onPlatformChange(value: string): void {
  const next = PLATFORMS.find((item) => item.value === value)
  if (!next || next.value === platform.value) return
  platform.value = next.value
  accountMode.value = ''
  baseUrl.value = ''
  groupIds.value = []
}

/**
 * 只提交同平台的分组。分组列表没拉到时（props.groups 为空）不做过滤，
 * 否则改一次备注就会把账号已有的分组绑定悄悄清空。
 */
function selectedGroupIds(): number[] {
  if (props.groups.length === 0) return groupIds.value
  const allowed = new Set(platformGroups.value.map((group) => group.id))
  return groupIds.value.filter((id) => allowed.has(id))
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
  const selectedPlatform = platform.value
  // valid 已经保证平台非空，这里只是把类型收窄回字面量联合。
  if (!selectedPlatform) return
  submitting.value = true
  serverError.value = ''
  try {
    const expires = clearExpiry.value ? '' : fromLocalInputValue(expiresAt.value) || undefined
    const key = apiKey.value.trim()
    const groups = selectedGroupIds()
    // Command Code 没有这两个字段，显式传空串，不给后端留猜测空间。
    const mode = usesMode.value ? accountMode.value : ''
    const url = usesMode.value ? baseUrl.value.trim() : ''

    if (isEdit.value && props.account) {
      const payload: UpdateAccountPayload = {
        name: name.value.trim(),
        account_mode: mode,
        base_url: url,
        notes: notes.value,
        concurrency: toNumber(concurrency.value),
        priority: toNumber(priority.value),
        rate_multiplier: toNumber(multiplier.value),
        status: status.value,
        schedulable: schedulable.value,
        group_ids: groups,
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
        platform: selectedPlatform,
        account_mode: mode,
        base_url: url,
        notes: notes.value,
        api_key: key,
        concurrency: toNumber(concurrency.value),
        priority: toNumber(priority.value),
        rate_multiplier: toNumber(multiplier.value),
        group_ids: groups,
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
    :subtitle="subtitle"
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

      <!-- 平台决定表单后面所有字段的形态，所以放在最前面且做成可点选的卡片 -->
      <Field
        label="上游平台"
        :required="!isEdit"
        :error="errors.platform"
        :hint="isEdit ? '平台创建后不可更改' : '平台决定密钥格式、计费模式与可绑定的分组'"
      >
        <PlatformPicker
          :model-value="platform"
          :disabled="isEdit"
          :invalid="Boolean(errors.platform)"
          @update:model-value="onPlatformChange"
        />
      </Field>

      <div class="grid gap-3.5 sm:grid-cols-2">
        <Field label="账号名称" required :error="errors.name">
          <Input v-model="name" placeholder="例如：主力账号 A" maxlength="100" />
        </Field>

        <Field
          label="API Key"
          :required="!isEdit"
          :error="errors.apiKey"
          :hint="isEdit ? '留空表示不修改当前密钥' : meta.keyHint"
        >
          <div class="relative">
            <Input
              v-model="apiKey"
              :type="showKey ? 'text' : 'password'"
              :mono="true"
              :placeholder="isEdit ? '••••••••（留空不修改）' : meta.keyPlaceholder"
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

      <!-- OpenCode 专属：计费模式必填，上游地址留空则由后端按模式回填 -->
      <template v-if="usesMode">
        <Field
          label="计费模式"
          required
          :error="errors.accountMode"
          hint="决定计费口径与默认上游地址"
        >
          <AccountModePicker v-model="accountMode" :invalid="Boolean(errors.accountMode)" />
        </Field>

        <Field label="上游地址" :hint="`留空使用默认地址 ${defaultBaseUrl}`">
          <Input
            v-model="baseUrl"
            :mono="true"
            :placeholder="defaultBaseUrl"
            autocomplete="off"
          />
        </Field>
      </template>

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
        hint="只能绑定同平台的分组；不选分组时该账号不会被任何下游密钥使用"
      >
        <!-- 平台没选之前不知道要列哪些分组，先占个位说明规则 -->
        <div
          v-if="!platform"
          class="rounded-md border border-line bg-raised/40 px-2.5 py-2 text-2xs text-subtle"
        >
          请先选择上游平台，这里只会列出同平台的分组。
        </div>

        <div
          v-else-if="platformGroups.length === 0"
          class="space-y-1.5 rounded-md border border-warning/30 bg-warning/10 px-2.5 py-2 text-2xs text-warning"
        >
          <p>
            还没有 {{ meta.label }} 平台的分组，请先到「分组」页创建一个，否则该账号无法被下游调用。
          </p>
          <RouterLink
            :to="{ name: 'groups' }"
            class="inline-flex items-center gap-1 font-medium text-accent hover:underline"
            @click="close"
          >
            去创建 {{ meta.label }} 分组
            <Icon name="external" :size="11" />
          </RouterLink>
        </div>

        <div v-else class="rounded-md border border-line bg-raised/40 p-2.5">
          <div class="scroll-thin max-h-40 space-y-2 overflow-y-auto">
            <Checkbox
              v-for="group in platformGroups"
              :key="group.id"
              :model-value="groupIds.includes(group.id)"
              :label="group.name"
              :description="`${group.account_count} 个账号 · ${group.api_key_count} 把密钥`"
              @update:model-value="toggleGroup(group.id, $event)"
            />
          </div>
          <p v-if="hiddenGroupCount > 0" class="mt-1.5 text-2xs text-subtle">
            另有 {{ hiddenGroupCount }} 个其它平台的分组未列出。
          </p>
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
