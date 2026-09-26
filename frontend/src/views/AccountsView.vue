<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import AccountFormModal from '@/components/accounts/AccountFormModal.vue'
import BatchImportModal from '@/components/accounts/BatchImportModal.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Pagination from '@/components/ui/Pagination.vue'
import Select from '@/components/ui/Select.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { accountsApi, groupsApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { Account, Group } from '@/api/types'
import { useToastStore } from '@/stores/toast'
import { formatDateTime, formatExpiry, formatNumber, formatRelative, isExpired } from '@/utils/format'
import { platformMeta } from '@/utils/platforms'

const toast = useToastStore()

const items = ref<Account[]>([])
const groups = ref<Group[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref('')

const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')
const status = ref('')
const groupFilter = ref('')
const platformFilter = ref('')

const formOpen = ref(false)
const editing = ref<Account | null>(null)
const batchOpen = ref(false)
const deleteTarget = ref<Account | null>(null)
const deleting = ref(false)
/** 正在探活的账号 id。探活会真的打上游，必须给出可见的进行中状态。 */
const checkingId = ref<number | null>(null)
const togglingId = ref<number | null>(null)

const statusOptions = [
  { label: '正常', value: 'active' },
  { label: '已禁用', value: 'disabled' },
  { label: '异常', value: 'error' },
]

// 与后端 ?platform= 一一对应，空串表示不筛选。
const platformOptions = [
  { label: 'Command Code', value: 'commandcode' },
  { label: 'OpenCode', value: 'opencode' },
]

const groupOptions = computed(() => groups.value.map((g) => ({ label: g.name, value: String(g.id) })))

const filtersActive = computed(
  () =>
    Boolean(keyword.value.trim()) ||
    Boolean(status.value) ||
    Boolean(groupFilter.value) ||
    Boolean(platformFilter.value),
)

const groupNameById = computed(() => {
  const map = new Map<number, string>()
  for (const group of groups.value) map.set(group.id, group.name)
  return map
})

const emptyDescription = computed(() =>
  filtersActive.value
    ? '当前筛选条件下没有账号，试试放宽关键词或清空筛选。'
    : 'cmd2api 需要至少一个上游账号才能发起请求：Command Code 账号用 user_ 开头的密钥，OpenCode 账号则要选好计费模式。可以逐个添加，也可以把多把密钥一次性粘进批量导入。',
)

let searchTimer: ReturnType<typeof setTimeout> | null = null

async function loadGroups(): Promise<void> {
  try {
    const response = await groupsApi.list()
    groups.value = response.items
  } catch (err) {
    // 分组拉不到不该让整页挂掉：账号列表本身仍然有意义。
    toast.error('分组列表加载失败', toMessage(err))
  }
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const response = await accountsApi.list({
      page: page.value,
      page_size: pageSize.value,
      status: status.value || undefined,
      keyword: keyword.value.trim() || undefined,
      group_id: groupFilter.value ? Number(groupFilter.value) : undefined,
      platform: platformFilter.value || undefined,
    })
    items.value = response.items
    total.value = response.total
  } catch (err) {
    error.value = toMessage(err, '加载账号列表失败')
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function reload(): void {
  void load()
}

watch(keyword, () => {
  if (searchTimer) clearTimeout(searchTimer)
  // 输入框每敲一下都打一次接口既浪费也会让表格闪，防抖 300ms。
  searchTimer = setTimeout(() => {
    page.value = 1
    void load()
  }, 300)
})

watch([status, groupFilter, platformFilter], () => {
  page.value = 1
  void load()
})

watch([page, pageSize], () => {
  void load()
})

onMounted(async () => {
  await Promise.all([load(), loadGroups()])
})

onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer)
})

function openCreate(): void {
  editing.value = null
  formOpen.value = true
}

function openEdit(account: Account): void {
  editing.value = account
  formOpen.value = true
}

function clearFilters(): void {
  keyword.value = ''
  status.value = ''
  groupFilter.value = ''
  platformFilter.value = ''
  page.value = 1
  void load()
}

async function check(account: Account): Promise<void> {
  if (checkingId.value !== null) return
  checkingId.value = account.id
  try {
    const result = await accountsApi.check(account.id)
    if (result.ok) {
      toast.success(`「${account.name}」连接正常`, `耗时 ${result.latency_ms} ms`)
    } else {
      toast.error(`「${account.name}」连接失败`, result.error ?? '上游未返回可用响应')
    }
    await load()
  } catch (err) {
    toast.error('测试连接失败', toMessage(err))
  } finally {
    checkingId.value = null
  }
}

async function toggleStatus(account: Account): Promise<void> {
  if (togglingId.value !== null) return
  const next = account.status === 'active' ? 'disabled' : 'active'
  togglingId.value = account.id
  try {
    await accountsApi.update(account.id, { status: next })
    toast.success(next === 'active' ? '账号已启用' : '账号已禁用', account.name)
    await load()
  } catch (err) {
    toast.error('修改状态失败', toMessage(err))
  } finally {
    togglingId.value = null
  }
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target) return
  deleting.value = true
  try {
    await accountsApi.remove(target.id)
    toast.success('账号已删除', target.name)
    deleteTarget.value = null
    // 删掉当前页最后一条时要回退一页，否则会停在空列表上。
    if (items.value.length === 1 && page.value > 1) page.value -= 1
    else await load()
  } catch (err) {
    toast.error('删除账号失败', toMessage(err))
  } finally {
    deleting.value = false
  }
}

/** 健康状态：优先看最近一次探活结论，没探过就显示未检测。 */
function healthOf(account: Account): { tone: 'success' | 'danger' | 'neutral'; text: string } {
  if (!account.last_health_check_at) return { tone: 'neutral', text: '未检测' }
  if (account.last_health_check_ok) {
    return {
      tone: 'success',
      text: account.latency_ms !== null ? `正常 ${account.latency_ms} ms` : '正常',
    }
  }
  return { tone: 'danger', text: '异常' }
}
</script>

<template>
  <div class="space-y-3">
    <!-- 工具条 -->
    <div class="flex flex-wrap items-end gap-2.5">
      <div class="w-full min-w-[12rem] sm:w-56">
        <Input v-model="keyword" size="sm" placeholder="搜索账号名称" />
      </div>

      <div class="w-32">
        <Select v-model="status" size="sm" :options="statusOptions" placeholder="全部状态" />
      </div>

      <div class="w-40">
        <Select
          v-model="platformFilter"
          size="sm"
          :options="platformOptions"
          placeholder="全部平台"
        />
      </div>

      <div class="w-40">
        <Select
          v-model="groupFilter"
          size="sm"
          :options="groupOptions"
          placeholder="全部分组"
        />
      </div>

      <Button v-if="filtersActive" variant="ghost" size="sm" @click="clearFilters">
        清空筛选
      </Button>

      <div class="ml-auto flex flex-wrap items-center gap-2">
        <Button variant="secondary" size="sm" :loading="loading" @click="reload">
          <template #leading><Icon name="refresh" :size="13" /></template>
          刷新
        </Button>
        <Button variant="secondary" size="sm" @click="batchOpen = true">
          <template #leading><Icon name="upload" :size="13" /></template>
          批量导入
        </Button>
        <Button variant="primary" size="sm" @click="openCreate">
          <template #leading><Icon name="plus" :size="13" /></template>
          添加账号
        </Button>
      </div>
    </div>

    <p
      v-if="error"
      class="flex items-center gap-2 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-2xs text-danger"
    >
      <Icon name="alertCircle" :size="13" />
      {{ error }}
      <Button variant="ghost" size="xs" class="ml-auto" @click="reload">重试</Button>
    </p>

    <DataTable
      :columns="10"
      :loading="loading"
      :empty="items.length === 0"
      :skeleton-rows="8"
      min-width="1240px"
      hoverable
    >
      <template #head>
        <th class="th">账号</th>
        <th class="th">状态</th>
        <th class="th">分组</th>
        <th class="th text-right">优先级</th>
        <th class="th text-right">并发</th>
        <th class="th text-right">倍率</th>
        <th class="th">健康</th>
        <th class="th">最近使用</th>
        <th class="th">过期时间</th>
        <th class="th text-right">操作</th>
      </template>

      <template #empty>
        <EmptyState
          icon="layers"
          :title="filtersActive ? '没有匹配的账号' : '还没有任何上游账号'"
          :description="emptyDescription"
        >
          <template v-if="filtersActive">
            <Button variant="secondary" size="sm" @click="clearFilters">清空筛选</Button>
          </template>
          <template v-else>
            <Button variant="primary" size="sm" @click="openCreate">
              <template #leading><Icon name="plus" :size="13" /></template>
              添加账号
            </Button>
            <Button variant="secondary" size="sm" @click="batchOpen = true">
              <template #leading><Icon name="upload" :size="13" /></template>
              批量导入
            </Button>
          </template>
        </EmptyState>
      </template>

      <template #body>
        <tr v-for="account in items" :key="account.id">
          <td class="td max-w-[16rem]">
            <div class="flex flex-col gap-0.5">
              <div class="flex items-center gap-1.5">
                <span class="truncate font-medium text-fg">{{ account.name }}</span>
                <!-- 平台用色调区分：Command Code 是主色，OpenCode 是信息色 -->
                <Badge :tone="platformMeta(account.platform).tone" dot>
                  {{ platformMeta(account.platform).label }}
                </Badge>
                <Badge v-if="!account.schedulable" tone="warning">不调度</Badge>
              </div>
              <div class="flex items-center gap-1.5 text-2xs">
                <template v-if="account.masked_key">
                  <span class="font-mono text-subtle">{{ account.masked_key }}</span>
                </template>
                <template v-else>
                  <span
                    class="inline-flex items-center gap-1 text-warning"
                    title="库里的凭证无法解密（通常是换了加密密钥）。请重新填写 API Key。"
                  >
                    <Icon name="alert" :size="11" />
                    凭证无法解密
                  </span>
                </template>
              </div>
              <p v-if="account.notes" class="truncate text-2xs text-subtle">{{ account.notes }}</p>
            </div>
          </td>

          <td class="td">
            <div class="flex flex-col items-start gap-1">
              <StatusPill
                :status="account.status"
                :label="account.status === 'active' ? '正常' : undefined"
              />
              <span
                v-if="account.error_message"
                class="max-w-[10rem] truncate text-2xs text-danger"
                :title="account.error_message"
              >
                {{ account.error_message }}
              </span>
            </div>
          </td>

          <td class="td max-w-[10rem]">
            <div v-if="account.group_ids.length" class="flex flex-wrap gap-1">
              <Badge
                v-for="groupId in account.group_ids.slice(0, 2)"
                :key="groupId"
                tone="neutral"
              >
                {{ groupNameById.get(groupId) ?? `#${groupId}` }}
              </Badge>
              <Badge v-if="account.group_ids.length > 2" tone="neutral">
                +{{ account.group_ids.length - 2 }}
              </Badge>
            </div>
            <span v-else class="text-2xs text-warning">未绑定分组</span>
          </td>

          <td class="td tnum text-right">{{ account.priority }}</td>
          <td class="td tnum text-right">{{ account.concurrency }}</td>
          <td class="td tnum text-right">{{ account.rate_multiplier }}</td>

          <td class="td">
            <div class="flex flex-col gap-0.5">
              <span
                class="inline-flex items-center gap-1 text-2xs"
                :class="{
                  'text-success': healthOf(account).tone === 'success',
                  'text-danger': healthOf(account).tone === 'danger',
                  'text-subtle': healthOf(account).tone === 'neutral',
                }"
              >
                <span
                  class="h-1.5 w-1.5 rounded-full bg-current"
                  aria-hidden="true"
                />
                {{ healthOf(account).text }}
              </span>
              <span
                v-if="account.last_health_check_error"
                class="max-w-[10rem] truncate text-2xs text-subtle"
                :title="account.last_health_check_error"
              >
                {{ account.last_health_check_error }}
              </span>
              <span v-else-if="account.last_health_check_at" class="text-2xs text-subtle">
                {{ formatRelative(account.last_health_check_at) }}
              </span>
              <span v-if="account.consecutive_failures > 0" class="text-2xs text-warning">
                连续失败 {{ account.consecutive_failures }} 次
              </span>
            </div>
          </td>

          <td class="td text-muted">{{ formatRelative(account.last_used_at) }}</td>

          <td class="td">
            <span
              class="text-2xs"
              :class="account.expires_at && isExpired(account.expires_at) ? 'text-danger' : 'text-muted'"
              :title="account.expires_at ? formatDateTime(account.expires_at) : ''"
            >
              {{ formatExpiry(account.expires_at) }}
            </span>
          </td>

          <td class="td">
            <div class="flex items-center justify-end gap-1">
              <Button
                variant="secondary"
                size="xs"
                :disabled="checkingId !== null"
                title="向上游发一次极小请求验证账号可用性（会消耗少量 token）"
                @click="check(account)"
              >
                <template #leading>
                  <Spinner v-if="checkingId === account.id" :size="11" />
                  <Icon v-else name="zap" :size="12" />
                </template>
                测试连接
              </Button>

              <Button
                variant="ghost"
                size="xs"
                icon
                :title="account.status === 'active' ? '禁用该账号' : '启用该账号'"
                :disabled="togglingId !== null"
                @click="toggleStatus(account)"
              >
                <Spinner v-if="togglingId === account.id" :size="11" />
                <Icon v-else :name="account.status === 'active' ? 'lock' : 'checkCircle'" :size="13" />
              </Button>

              <Button variant="ghost" size="xs" icon title="编辑" @click="openEdit(account)">
                <Icon name="edit" :size="13" />
              </Button>

              <Button
                variant="ghost"
                size="xs"
                icon
                class="hover:bg-danger/10 hover:text-danger"
                title="删除"
                @click="deleteTarget = account"
              >
                <Icon name="trash" :size="13" />
              </Button>
            </div>
          </td>
        </tr>
      </template>

      <template #footer>
        <Pagination
          v-model:page="page"
          v-model:page-size="pageSize"
          :total="total"
        />
      </template>
    </DataTable>

    <p class="px-1 text-2xs text-subtle">
      共 {{ formatNumber(total) }} 个账号。优先级数值越小越先被调度；「测试连接」会真实访问上游并消耗少量
      token，请勿频繁点击。
    </p>

    <AccountFormModal
      v-model:open="formOpen"
      :account="editing"
      :groups="groups"
      @saved="reload"
    />

    <BatchImportModal v-model:open="batchOpen" :groups="groups" @imported="reload" />

    <ConfirmDialog
      :open="deleteTarget !== null"
      title="删除账号"
      :message="`确定删除「${deleteTarget?.name ?? ''}」吗？该账号的历史用量记录会保留，但它将不再参与调度。`"
      confirm-text="删除"
      tone="danger"
      :loading="deleting"
      @update:open="deleteTarget = null"
      @confirm="confirmDelete"
    />
  </div>
</template>
