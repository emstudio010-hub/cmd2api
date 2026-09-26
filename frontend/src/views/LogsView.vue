<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Modal from '@/components/ui/Modal.vue'
import Pagination from '@/components/ui/Pagination.vue'
import Select from '@/components/ui/Select.vue'
import { accountsApi, keysApi, logsApi, metaApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { Account, ApiKey, ModelInfo, UsageLog } from '@/api/types'
import { useTimeRange } from '@/composables/useTimeRange'
import { formatCompact, formatCost, formatDateTime, formatDuration, formatNumber } from '@/utils/format'

const items = ref<UsageLog[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref('')

const page = ref(1)
const pageSize = ref(50)

// ---- 筛选条件 ----
const {
  mode: rangeMode,
  preset: presetHours,
  presetOptions,
  customStart,
  customEnd,
  error: rangeError,
  range,
  useCustom,
  usePreset,
} = useTimeRange(24)

const statusFilter = ref('')
const modelFilter = ref('')
const requestIdFilter = ref('')
const apiKeyFilter = ref('')
const accountFilter = ref('')

// ---- 辅助数据（下拉选项）----
const models = ref<ModelInfo[]>([])
const modelsError = ref('')
const apiKeys = ref<ApiKey[]>([])
const accounts = ref<Account[]>([])

const detail = ref<UsageLog | null>(null)

const statusOptions = [
  { label: '成功（<400）', value: 'ok' },
  { label: '失败（>=400）', value: 'error' },
]

const keyOptions = computed(() =>
  apiKeys.value.map((key) => ({ label: `${key.name}${key.group_name ? ` · ${key.group_name}` : ''}`, value: String(key.id) })),
)

const accountOptions = computed(() =>
  accounts.value.map((account) => ({ label: account.name, value: String(account.id) })),
)

const filtersActive = computed(
  () =>
    Boolean(statusFilter.value) ||
    Boolean(modelFilter.value.trim()) ||
    Boolean(requestIdFilter.value.trim()) ||
    Boolean(apiKeyFilter.value) ||
    Boolean(accountFilter.value),
)

let searchTimer: ReturnType<typeof setTimeout> | null = null

async function load(): Promise<void> {
  if (rangeError.value) return
  loading.value = true
  error.value = ''
  try {
    const response = await logsApi.list({
      page: page.value,
      page_size: pageSize.value,
      start: range.value.start,
      end: range.value.end,
      status: (statusFilter.value || undefined) as 'ok' | 'error' | undefined,
      model: modelFilter.value.trim() || undefined,
      request_id: requestIdFilter.value.trim() || undefined,
      api_key_id: apiKeyFilter.value ? Number(apiKeyFilter.value) : undefined,
      account_id: accountFilter.value ? Number(accountFilter.value) : undefined,
    })
    items.value = response.items
    total.value = response.total
  } catch (err) {
    error.value = toMessage(err, '加载日志失败')
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

/** 筛选条件变化一律回到第一页，否则很容易停在越界的页上看到空列表。 */
function applyFilters(): void {
  page.value = 1
  void load()
}

async function loadAuxiliary(): Promise<void> {
  // 这三份数据只用来填充筛选下拉框，失败不该影响主列表，所以分别 catch。
  try {
    const response = await keysApi.list()
    apiKeys.value = response.items
  } catch {
    apiKeys.value = []
  }
  try {
    const response = await accountsApi.list({ page: 1, page_size: 200 })
    accounts.value = response.items
  } catch {
    accounts.value = []
  }
  try {
    const response = await metaApi.models()
    models.value = response.items
    modelsError.value = response.hint ?? ''
  } catch (err) {
    // /api/models 在没有可用账号时返回 503，这是正常状态，提示一下即可。
    models.value = []
    modelsError.value = toMessage(err, '暂时无法获取模型列表')
  }
}

onMounted(() => {
  void load()
  void loadAuxiliary()
})

watch([statusFilter, apiKeyFilter, accountFilter, presetHours], () => {
  if (rangeMode.value === 'preset') applyFilters()
})

watch([page, pageSize], () => {
  void load()
})

// 自定义时间范围用「应用」按钮触发；文本类筛选用回车或查询按钮触发。
watch(requestIdFilter, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(applyFilters, 400)
})

onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer)
})

function resetFilters(): void {
  statusFilter.value = ''
  modelFilter.value = ''
  requestIdFilter.value = ''
  apiKeyFilter.value = ''
  accountFilter.value = ''
  usePreset()
  presetHours.value = '24'
  page.value = 1
  void load()
}

function backToPreset(): void {
  usePreset()
  applyFilters()
}

function statusTone(code: number): 'success' | 'warning' | 'danger' {
  if (code < 400) return 'success'
  if (code < 500) return 'warning'
  return 'danger'
}

/** 详情弹窗里的字段清单。显式标注类型，让 mono 成为可缺省字段。 */
interface DetailRow {
  label: string
  value: string
  mono?: boolean
}

const detailRows = computed<DetailRow[]>(() => {
  const row = detail.value
  if (!row) return []
  return [
    { label: '时间', value: formatDateTime(row.created_at) },
    { label: '请求 ID', value: row.request_id, mono: true },
    { label: '模型', value: row.model, mono: true },
    { label: '状态码', value: String(row.status_code) },
    { label: '错误信息', value: row.error_message || '—' },
    { label: '下游密钥', value: row.api_key_name ? `${row.api_key_name} (#${row.api_key_id})` : `#${row.api_key_id}` },
    { label: '上游账号', value: row.account_name ? `${row.account_name} (#${row.account_id})` : `#${row.account_id}` },
    { label: '分组 ID', value: row.group_id === null ? '—' : String(row.group_id) },
    { label: '用户 ID', value: String(row.user_id) },
    { label: '输入 Token', value: formatNumber(row.input_tokens) },
    { label: '输出 Token', value: formatNumber(row.output_tokens) },
    { label: '缓存写入 Token', value: formatNumber(row.cache_creation_tokens) },
    { label: '缓存读取 Token', value: formatNumber(row.cache_read_tokens) },
    { label: '费用', value: formatCost(row.total_cost) },
    { label: '费用倍率', value: String(row.rate_multiplier) },
    { label: '流式', value: row.stream ? '是' : '否' },
    { label: '总耗时', value: formatDuration(row.duration_ms) },
    { label: '首 Token 耗时', value: formatDuration(row.first_token_ms) },
    { label: '来源 IP', value: row.ip_address || '—', mono: true },
    { label: 'User-Agent', value: row.user_agent || '—' },
  ]
})
</script>

<template>
  <div class="space-y-3">
    <!-- 筛选区：字段多但都是运维排查时真正会用到的 -->
    <div class="card space-y-3 px-3 py-3">
      <div class="flex flex-wrap items-end gap-2.5">
        <div class="w-40">
          <Field label="时间范围">
            <Select
              v-model="presetHours"
              size="sm"
              :options="presetOptions"
              :disabled="rangeMode === 'custom'"
            />
          </Field>
        </div>

        <div class="flex items-center gap-2 pb-1">
          <Button v-if="rangeMode === 'custom'" variant="subtle" size="sm" @click="backToPreset">
            返回预设
          </Button>
          <Button v-else variant="ghost" size="sm" @click="useCustom(24)">
            <template #leading><Icon name="clock" :size="13" /></template>
            自定义范围
          </Button>
        </div>

        <div class="w-40">
          <Field label="状态">
            <Select v-model="statusFilter" size="sm" :options="statusOptions" placeholder="全部" />
          </Field>
        </div>

        <div class="w-48">
          <Field label="下游密钥">
            <Select v-model="apiKeyFilter" size="sm" :options="keyOptions" placeholder="全部密钥" />
          </Field>
        </div>

        <div class="w-48">
          <Field label="上游账号">
            <Select
              v-model="accountFilter"
              size="sm"
              :options="accountOptions"
              placeholder="全部账号"
            />
          </Field>
        </div>
      </div>

      <div v-if="rangeMode === 'custom'" class="flex flex-wrap items-end gap-2.5">
        <div class="w-52">
          <Field label="开始时间">
            <Input v-model="customStart" size="sm" type="datetime-local" />
          </Field>
        </div>
        <div class="w-52">
          <Field label="结束时间" :error="rangeError">
            <Input v-model="customEnd" size="sm" type="datetime-local" />
          </Field>
        </div>
        <div class="pb-1">
          <Button variant="primary" size="sm" :disabled="Boolean(rangeError)" @click="applyFilters">
            应用范围
          </Button>
        </div>
      </div>

      <div class="flex flex-wrap items-end gap-2.5">
        <div class="w-full sm:w-64">
          <Field
            label="模型"
            :hint="modelsError || (models.length ? '可从候选里选，也可以手填' : '')"
          >
            <Input
              v-model="modelFilter"
              size="sm"
              mono
              list="log-model-options"
              placeholder="例如 claude-sonnet-4-5"
              @keyup.enter="applyFilters"
            />
            <datalist id="log-model-options">
              <option v-for="model in models" :key="model.id" :value="model.id">
                {{ model.name }}
              </option>
            </datalist>
          </Field>
        </div>

        <div class="w-full sm:w-72">
          <Field label="请求 ID">
            <Input
              v-model="requestIdFilter"
              size="sm"
              mono
              placeholder="按请求 ID 精确定位"
              @keyup.enter="applyFilters"
            />
          </Field>
        </div>

        <div class="flex items-center gap-2 pb-1">
          <Button variant="primary" size="sm" :loading="loading" @click="applyFilters">
            <template #leading><Icon name="search" :size="13" /></template>
            查询
          </Button>
          <Button v-if="filtersActive" variant="ghost" size="sm" @click="resetFilters">
            重置
          </Button>
          <Button v-else variant="secondary" size="sm" @click="load">
            <template #leading><Icon name="refresh" :size="13" /></template>
            刷新
          </Button>
        </div>
      </div>
    </div>

    <p
      v-if="error"
      class="flex items-center gap-2 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-2xs text-danger"
    >
      <Icon name="alertCircle" :size="13" />
      {{ error }}
      <Button variant="ghost" size="xs" class="ml-auto" @click="load">重试</Button>
    </p>

    <DataTable
      :columns="9"
      :loading="loading"
      :empty="items.length === 0"
      :skeleton-rows="10"
      min-width="1320px"
      hoverable
    >
      <template #head>
        <th class="th">时间</th>
        <th class="th">状态</th>
        <th class="th">模型</th>
        <th class="th">下游密钥</th>
        <th class="th">上游账号</th>
        <th class="th text-right">Token（入 / 出 / 缓存）</th>
        <th class="th text-right">费用</th>
        <th class="th text-right">耗时</th>
        <th class="th">来源</th>
      </template>

      <template #empty>
        <EmptyState
          icon="activity"
          :title="filtersActive ? '没有符合条件的日志' : '所选时间范围内没有调用记录'"
          :description="
            filtersActive
              ? '试试放宽时间范围或清空筛选条件。'
              : '客户端还没有通过 cmd2api 发起过请求。确认下游密钥已签发给客户端后，这里会出现逐条记录。'
          "
        >
          <Button v-if="filtersActive" variant="secondary" size="sm" @click="resetFilters">
            重置筛选
          </Button>
        </EmptyState>
      </template>

      <template #body>
        <tr
          v-for="log in items"
          :key="log.id"
          class="cursor-pointer"
          @click="detail = log"
        >
          <td class="td tnum text-muted">{{ formatDateTime(log.created_at) }}</td>

          <td class="td">
            <Badge :tone="statusTone(log.status_code)" dot>
              {{ log.status_code }}
            </Badge>
          </td>

          <td class="td">
            <span class="font-mono text-2xs text-fg">{{ log.model || '—' }}</span>
          </td>

          <td class="td max-w-[12rem]">
            <span class="truncate text-muted">{{ log.api_key_name || `#${log.api_key_id}` }}</span>
          </td>

          <td class="td max-w-[12rem]">
            <span class="truncate text-muted">{{ log.account_name || `#${log.account_id}` }}</span>
          </td>

          <td class="td tnum text-right">
            <div class="flex items-center justify-end gap-1.5">
              <span class="text-fg">{{ formatCompact(log.input_tokens) }}</span>
              <span class="text-subtle">/</span>
              <span class="text-fg">{{ formatCompact(log.output_tokens) }}</span>
              <span class="text-subtle">/</span>
              <span class="text-subtle">{{ formatCompact(log.cache_read_tokens) }}</span>
            </div>
          </td>

          <td class="td tnum text-right text-fg">{{ formatCost(log.total_cost) }}</td>

          <td class="td tnum text-right">
            <div class="flex flex-col items-end">
              <span class="text-fg">{{ formatDuration(log.duration_ms) }}</span>
              <span v-if="log.first_token_ms !== null" class="text-2xs text-subtle">
                首 {{ formatDuration(log.first_token_ms) }}
              </span>
            </div>
          </td>

          <td class="td max-w-[10rem]">
            <div class="flex items-center gap-1.5">
              <Badge :tone="log.stream ? 'info' : 'neutral'">
                {{ log.stream ? '流式' : '非流式' }}
              </Badge>
              <span class="truncate font-mono text-2xs text-subtle">{{ log.ip_address || '—' }}</span>
            </div>
          </td>
        </tr>
      </template>

      <template #footer>
        <Pagination v-model:page="page" v-model:page-size="pageSize" :total="total" />
      </template>
    </DataTable>

    <p class="px-1 text-2xs text-subtle">
      共 {{ formatNumber(total) }} 条记录。点击任意一行查看完整详情（错误信息、User-Agent 等）。
    </p>

    <Modal
      :open="detail !== null"
      title="日志详情"
      :subtitle="detail ? `#${detail.id} · ${formatDateTime(detail.created_at)}` : ''"
      size="lg"
      @update:open="detail = null"
    >
      <div v-if="detail" class="space-y-3">
        <p
          v-if="detail.error_message"
          class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger/10 px-2.5 py-2 text-2xs leading-relaxed text-danger"
        >
          <Icon name="alertCircle" :size="13" class="mt-px" />
          <span class="break-all">{{ detail.error_message }}</span>
        </p>

        <dl class="grid gap-x-4 gap-y-2 sm:grid-cols-2">
          <div
            v-for="row in detailRows"
            :key="row.label"
            class="flex items-start justify-between gap-3 border-b border-line/60 pb-1.5"
          >
            <dt class="shrink-0 text-2xs text-subtle">{{ row.label }}</dt>
            <dd
              class="min-w-0 break-all text-right text-2xs text-fg"
              :class="row.mono ? 'font-mono' : ''"
            >
              {{ row.value }}
            </dd>
          </div>
        </dl>
      </div>
    </Modal>
  </div>
</template>
