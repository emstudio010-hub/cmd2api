<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import GroupFormModal from '@/components/groups/GroupFormModal.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Icon from '@/components/ui/Icon.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { groupsApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { Group } from '@/api/types'
import { useToastStore } from '@/stores/toast'
import { formatDateTime, formatNumber } from '@/utils/format'

const toast = useToastStore()

const items = ref<Group[]>([])
const loading = ref(true)
const error = ref('')

const formOpen = ref(false)
const editing = ref<Group | null>(null)
const deleteTarget = ref<Group | null>(null)
const deleting = ref(false)

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const response = await groupsApi.list()
    items.value = response.items
  } catch (err) {
    error.value = toMessage(err, '加载分组失败')
    items.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})

function openCreate(): void {
  editing.value = null
  formOpen.value = true
}

function openEdit(group: Group): void {
  editing.value = group
  formOpen.value = true
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target) return
  deleting.value = true
  try {
    await groupsApi.remove(target.id)
    toast.success('分组已删除', target.name)
    deleteTarget.value = null
    await load()
  } catch (err) {
    // 分组下还有密钥时后端返回 409，把服务端那句话原样透出来——
    // 「请先迁移或删除这些 Key」比任何前端兜底文案都有用。
    toast.error('删除分组失败', toMessage(err), 8000)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <div class="space-y-3">
    <div class="flex flex-wrap items-center justify-between gap-2.5">
      <p class="text-2xs text-subtle">
        分组把上游账号和下游密钥绑定在一起：密钥只能使用同组内的账号。
        大多数部署只需要一个默认分组。
      </p>
      <div class="flex items-center gap-2">
        <Button variant="secondary" size="sm" :loading="loading" @click="load">
          <template #leading><Icon name="refresh" :size="13" /></template>
          刷新
        </Button>
        <Button variant="primary" size="sm" @click="openCreate">
          <template #leading><Icon name="plus" :size="13" /></template>
          新建分组
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

    <DataTable :columns="8" :loading="loading" :empty="items.length === 0" min-width="1000px">
      <template #head>
        <th class="th">分组</th>
        <th class="th">描述</th>
        <th class="th text-right">倍率</th>
        <th class="th">状态</th>
        <th class="th text-right">账号数</th>
        <th class="th text-right">密钥数</th>
        <th class="th">创建时间</th>
        <th class="th text-right">操作</th>
      </template>

      <template #empty>
        <EmptyState
          icon="folder"
          title="还没有任何分组"
          description="第一次使用请先创建一个分组（例如「默认分组」），然后把上游账号绑定进去。没有分组时无法签发下游密钥。"
        >
          <Button variant="primary" size="sm" @click="openCreate">
            <template #leading><Icon name="plus" :size="13" /></template>
            新建分组
          </Button>
        </EmptyState>
      </template>

      <template #body>
        <tr v-for="group in items" :key="group.id">
          <td class="td max-w-[14rem]">
            <span class="truncate font-medium text-fg">{{ group.name }}</span>
          </td>
          <td class="td max-w-[22rem]">
            <span class="block truncate text-muted">{{ group.description || '—' }}</span>
          </td>
          <td class="td tnum text-right">{{ group.rate_multiplier }}</td>
          <td class="td">
            <StatusPill :status="group.status" />
          </td>
          <td class="td text-right">
            <RouterLink
              :to="{ name: 'accounts' }"
              class="tnum text-accent transition hover:underline"
            >
              {{ formatNumber(group.account_count) }}
            </RouterLink>
          </td>
          <td class="td text-right">
            <RouterLink
              :to="{ name: 'keys' }"
              class="tnum text-accent transition hover:underline"
            >
              {{ formatNumber(group.api_key_count) }}
            </RouterLink>
          </td>
          <td class="td text-muted">{{ formatDateTime(group.created_at) }}</td>
          <td class="td">
            <div class="flex items-center justify-end gap-1">
              <Button variant="ghost" size="xs" icon title="编辑" @click="openEdit(group)">
                <Icon name="edit" :size="13" />
              </Button>
              <Button
                variant="ghost"
                size="xs"
                icon
                class="hover:bg-danger/10 hover:text-danger"
                :title="group.api_key_count > 0 ? '该分组下还有密钥，需要先迁移' : '删除'"
                @click="deleteTarget = group"
              >
                <Icon name="trash" :size="13" />
              </Button>
            </div>
          </td>
        </tr>
      </template>
    </DataTable>

    <GroupFormModal v-model:open="formOpen" :group="editing" @saved="load" />

    <ConfirmDialog
      :open="deleteTarget !== null"
      title="删除分组"
      :message="`确定删除「${deleteTarget?.name ?? ''}」吗？`"
      confirm-text="删除"
      tone="danger"
      :loading="deleting"
      @update:open="deleteTarget = null"
      @confirm="confirmDelete"
    >
      <p v-if="deleteTarget && deleteTarget.api_key_count > 0" class="text-danger">
        该分组下还有 {{ deleteTarget.api_key_count }} 把下游密钥，后端会拒绝删除。
        请先把这些密钥改到别的分组或删除。
      </p>
      <p v-else class="text-muted">
        该分组下的账号不会被删除，但会失去分组绑定，从而不再被任何密钥使用。
      </p>
    </ConfirmDialog>
  </div>
</template>
