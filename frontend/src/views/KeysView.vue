<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import ApiKeyFormModal from '@/components/keys/ApiKeyFormModal.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { groupsApi, keysApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { ApiKey, Group } from '@/api/types'
import { useToastStore } from '@/stores/toast'
import { copyText } from '@/utils/clipboard'
import { formatDateTime, formatExpiry, formatNumber, formatRelative, isExpired } from '@/utils/format'

const toast = useToastStore()

const items = ref<ApiKey[]>([])
const groups = ref<Group[]>([])
const total = ref(0)
const loading = ref(true)
const error = ref('')

const keyword = ref('')
const status = ref('')
const groupFilter = ref('')

const formOpen = ref(false)
const editing = ref<ApiKey | null>(null)
/** 刚创建的密钥：明文只保证在这一刻完整可得，所以单独顶在最上面。 */
const justCreated = ref<ApiKey | null>(null)
const revealed = ref<number[]>([])
const resetTarget = ref<ApiKey | null>(null)
const resetting = ref(false)
const deleteTarget = ref<ApiKey | null>(null)
const deleting = ref(false)
const copiedId = ref<number | null>(null)

const statusOptions = [
  { label: '启用', value: 'active' },
  { label: '禁用', value: 'disabled' },
]

const groupOptions = computed(() => groups.value.map((g) => ({ label: g.name, value: String(g.id) })))

const filtersActive = computed(
  () => Boolean(keyword.value.trim()) || Boolean(status.value) || Boolean(groupFilter.value),
)

/** 后端 /api/keys 固定最多返回 200 条，超出部分要靠关键词收窄。 */
const truncated = computed(() => total.value > items.value.length)

let searchTimer: ReturnType<typeof setTimeout> | null = null

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const response = await keysApi.list({
      keyword: keyword.value.trim() || undefined,
      status: status.value || undefined,
      group_id: groupFilter.value ? Number(groupFilter.value) : undefined,
    })
    items.value = response.items
    total.value = response.total
    // 列表刷新后旧的展开状态可能指向已不存在的行，顺手清掉。
    const ids = new Set(response.items.map((item) => item.id))
    revealed.value = revealed.value.filter((id) => ids.has(id))
  } catch (err) {
    error.value = toMessage(err, '加载密钥列表失败')
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

async function loadGroups(): Promise<void> {
  try {
    const response = await groupsApi.list()
    groups.value = response.items
  } catch (err) {
    toast.error('分组列表加载失败', toMessage(err))
  }
}

onMounted(async () => {
  await Promise.all([load(), loadGroups()])
})

watch(keyword, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => void load(), 300)
})

watch([status, groupFilter], () => {
  void load()
})

function openCreate(): void {
  editing.value = null
  formOpen.value = true
}

function openEdit(item: ApiKey): void {
  editing.value = item
  formOpen.value = true
}

function clearFilters(): void {
  keyword.value = ''
  status.value = ''
  groupFilter.value = ''
  void load()
}

function onCreated(created: ApiKey): void {
  justCreated.value = created
  // 新建的密钥默认展开，省掉「还要再点一次眼睛」。
  if (!revealed.value.includes(created.id)) revealed.value = [...revealed.value, created.id]
}

function toggleReveal(id: number): void {
  revealed.value = revealed.value.includes(id)
    ? revealed.value.filter((item) => item !== id)
    : [...revealed.value, id]
}

async function copy(value: string, id: number): Promise<void> {
  const ok = await copyText(value)
  if (ok) {
    copiedId.value = id
    setTimeout(() => {
      if (copiedId.value === id) copiedId.value = null
    }, 1600)
    toast.success('已复制到剪贴板')
  } else {
    toast.error('复制失败', '请手动选中密钥文本复制')
  }
}

function quotaPercent(item: ApiKey): number {
  if (item.quota <= 0) return 0
  return Math.min((item.quota_used / item.quota) * 100, 100)
}

function quotaTone(item: ApiKey): string {
  const percent = quotaPercent(item)
  if (percent >= 100) return 'bg-danger'
  if (percent >= 80) return 'bg-warning'
  return 'bg-accent'
}

async function confirmReset(): Promise<void> {
  const target = resetTarget.value
  if (!target) return
  resetting.value = true
  try {
    await keysApi.resetQuota(target.id)
    toast.success('已重置已用额度', target.name)
    resetTarget.value = null
    await load()
  } catch (err) {
    toast.error('重置额度失败', toMessage(err))
  } finally {
    resetting.value = false
  }
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target) return
  deleting.value = true
  try {
    await keysApi.remove(target.id)
    toast.success('密钥已删除', target.name)
    if (justCreated.value?.id === target.id) justCreated.value = null
    deleteTarget.value = null
    await load()
  } catch (err) {
    toast.error('删除密钥失败', toMessage(err))
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <div class="space-y-3">
    <!-- 没有分组时先挡住：后端会返回 400，不如在这里说清楚 -->
    <div
      v-if="!loading && groups.length === 0"
      class="flex flex-wrap items-center gap-2 rounded-panel border border-warning/30 bg-warning/10 px-3 py-2.5 text-2xs text-warning"
    >
      <Icon name="alert" :size="14" />
      <span>还没有任何分组，无法签发下游密钥。请先创建一个分组并把上游账号绑定进去。</span>
      <RouterLink :to="{ name: 'groups' }" class="ml-auto shrink-0">
        <Button variant="secondary" size="xs">去创建分组</Button>
      </RouterLink>
    </div>

    <!-- 刚创建的密钥 -->
    <div
      v-if="justCreated"
      class="rounded-panel border border-success/30 bg-success/[0.08] px-3 py-2.5"
    >
      <div class="flex flex-wrap items-center gap-2">
        <Icon name="checkCircle" :size="14" class="text-success" />
        <span class="text-[13px] font-medium text-fg">
          「{{ justCreated.name }}」已创建
        </span>
        <span class="text-2xs text-subtle">请立即复制保存，密钥明文只在列表里可见</span>
        <div class="ml-auto flex items-center gap-2">
          <Button variant="secondary" size="xs" @click="copy(justCreated.key, justCreated.id)">
            <template #leading><Icon name="copy" :size="12" /></template>
            复制密钥
          </Button>
          <Button variant="ghost" size="xs" icon title="收起" @click="justCreated = null">
            <Icon name="close" :size="13" />
          </Button>
        </div>
      </div>
      <p class="mt-2 break-all rounded-md border border-line bg-surface px-2.5 py-2 font-mono text-2xs text-fg">
        {{ justCreated.key }}
      </p>
    </div>

    <div class="flex flex-wrap items-center gap-2.5">
      <div class="w-full min-w-[12rem] sm:w-56">
        <Input v-model="keyword" size="sm" placeholder="搜索密钥名称" />
      </div>

      <div class="w-32">
        <Select v-model="status" size="sm" :options="statusOptions" placeholder="全部状态" />
      </div>

      <div class="w-40">
        <Select v-model="groupFilter" size="sm" :options="groupOptions" placeholder="全部分组" />
      </div>

      <Button v-if="filtersActive" variant="ghost" size="sm" @click="clearFilters">
        清空筛选
      </Button>

      <div class="ml-auto flex items-center gap-2">
        <Button variant="secondary" size="sm" :loading="loading" @click="load">
          <template #leading><Icon name="refresh" :size="13" /></template>
          刷新
        </Button>
        <Button
          variant="primary"
          size="sm"
          :disabled="groups.length === 0"
          :title="groups.length === 0 ? '请先创建分组' : ''"
          @click="openCreate"
        >
          <template #leading><Icon name="plus" :size="13" /></template>
          签发密钥
        </Button>
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

    <DataTable :columns="9" :loading="loading" :empty="items.length === 0" min-width="1180px">
      <template #head>
        <th class="th">名称</th>
        <th class="th">密钥</th>
        <th class="th">分组</th>
        <th class="th">状态</th>
        <th class="th w-[13rem]">额度</th>
        <th class="th">最近使用</th>
        <th class="th">过期时间</th>
        <th class="th">创建时间</th>
        <th class="th text-right">操作</th>
      </template>

      <template #empty>
        <EmptyState
          icon="key"
          :title="filtersActive ? '没有匹配的密钥' : '还没有下游密钥'"
          :description="
            filtersActive
              ? '当前筛选条件下没有密钥，试试换个关键词。'
              : '下游客户端（ChatGPT 客户端、Claude Code 等）用这把密钥访问 /v1 接口。签发时需要选择一个分组。'
          "
        >
          <template v-if="filtersActive">
            <Button variant="secondary" size="sm" @click="clearFilters">清空筛选</Button>
          </template>
          <template v-else>
            <Button
              variant="primary"
              size="sm"
              :disabled="groups.length === 0"
              @click="openCreate"
            >
              <template #leading><Icon name="plus" :size="13" /></template>
              签发密钥
            </Button>
          </template>
        </EmptyState>
      </template>

      <template #body>
        <tr v-for="item in items" :key="item.id">
          <td class="td max-w-[12rem]">
            <div class="flex flex-col gap-0.5">
              <span class="truncate font-medium text-fg">{{ item.name }}</span>
              <span class="tnum text-2xs text-subtle">#{{ item.id }}</span>
            </div>
          </td>

          <td class="td">
            <div class="flex items-center gap-1.5">
              <code class="max-w-[16rem] truncate font-mono text-2xs text-muted">
                {{ revealed.includes(item.id) ? item.key : `${item.key.slice(0, 10)}••••••••` }}
              </code>
              <button
                type="button"
                class="rounded p-0.5 text-subtle transition hover:text-fg"
                :aria-label="revealed.includes(item.id) ? '隐藏密钥' : '显示密钥'"
                @click="toggleReveal(item.id)"
              >
                <Icon :name="revealed.includes(item.id) ? 'eyeOff' : 'eye'" :size="13" />
              </button>
              <button
                type="button"
                class="rounded p-0.5 transition hover:text-fg"
                :class="copiedId === item.id ? 'text-success' : 'text-subtle'"
                aria-label="复制密钥"
                @click="copy(item.key, item.id)"
              >
                <Icon :name="copiedId === item.id ? 'check' : 'copy'" :size="13" />
              </button>
            </div>
          </td>

          <td class="td">
            <Badge v-if="item.group_name" tone="neutral">{{ item.group_name }}</Badge>
            <span v-else class="text-2xs text-warning">未绑定分组</span>
          </td>

          <td class="td">
            <div class="flex flex-col items-start gap-1">
              <StatusPill :status="item.status" />
              <span
                v-if="item.expires_at && isExpired(item.expires_at)"
                class="text-2xs text-danger"
              >
                已过期
              </span>
            </div>
          </td>

          <td class="td">
            <div v-if="item.quota > 0" class="space-y-1">
              <div class="flex items-center justify-between gap-2 text-2xs">
                <span class="tnum text-muted">
                  {{ formatNumber(item.quota_used) }} / {{ formatNumber(item.quota) }}
                </span>
                <span class="tnum text-subtle">{{ quotaPercent(item).toFixed(1) }}%</span>
              </div>
              <div class="h-1.5 w-full overflow-hidden rounded-full bg-raised">
                <div
                  class="h-full rounded-full"
                  :class="quotaTone(item)"
                  :style="{ width: `${quotaPercent(item)}%` }"
                />
              </div>
            </div>
            <Badge v-else tone="neutral">不限额</Badge>
          </td>

          <td class="td text-muted">{{ formatRelative(item.last_used_at) }}</td>

          <td class="td">
            <span
              class="text-2xs"
              :class="item.expires_at && isExpired(item.expires_at) ? 'text-danger' : 'text-muted'"
              :title="item.expires_at ? formatDateTime(item.expires_at) : ''"
            >
              {{ formatExpiry(item.expires_at) }}
            </span>
          </td>

          <td class="td text-muted">{{ formatDateTime(item.created_at) }}</td>

          <td class="td">
            <div class="flex items-center justify-end gap-1">
              <Button
                variant="ghost"
                size="xs"
                :disabled="item.quota <= 0"
                :title="item.quota > 0 ? '把已用额度清零' : '不限额的密钥无需重置'"
                @click="resetTarget = item"
              >
                重置额度
              </Button>
              <Button variant="ghost" size="xs" icon title="编辑" @click="openEdit(item)">
                <Icon name="edit" :size="13" />
              </Button>
              <Button
                variant="ghost"
                size="xs"
                icon
                class="hover:bg-danger/10 hover:text-danger"
                title="删除"
                @click="deleteTarget = item"
              >
                <Icon name="trash" :size="13" />
              </Button>
            </div>
          </td>
        </tr>
      </template>

      <template #footer>
        <div class="flex flex-wrap items-center gap-2 border-t border-line px-3 py-2 text-2xs text-subtle">
          <span class="tnum">共 {{ formatNumber(total) }} 把密钥</span>
          <span v-if="truncated" class="text-warning">
            （列表最多展示 200 条，请用名称关键词收窄范围）
          </span>
        </div>
      </template>
    </DataTable>

    <ApiKeyFormModal
      v-model:open="formOpen"
      :api-key="editing"
      :groups="groups"
      @saved="load"
      @created="onCreated"
    />

    <ConfirmDialog
      :open="resetTarget !== null"
      title="重置已用额度"
      :message="`将把「${resetTarget?.name ?? ''}」的已用额度清零（当前已用 ${formatNumber(resetTarget?.quota_used ?? 0)}）。此操作不可撤销。`"
      confirm-text="重置"
      :loading="resetting"
      @update:open="resetTarget = null"
      @confirm="confirmReset"
    />

    <ConfirmDialog
      :open="deleteTarget !== null"
      title="删除密钥"
      :message="`确定删除「${deleteTarget?.name ?? ''}」吗？删除后使用该密钥的客户端会立即收到 401。`"
      confirm-text="删除"
      tone="danger"
      :loading="deleting"
      @update:open="deleteTarget = null"
      @confirm="confirmDelete"
    />
  </div>
</template>
