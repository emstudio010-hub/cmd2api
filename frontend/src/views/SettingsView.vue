<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Icon from '@/components/ui/Icon.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import { metaApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { ModelInfo, SettingsResponse } from '@/api/types'

interface SettingRow {
  label: string
  value: string
  hint?: string
  mono?: boolean
  tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent'
}

const settings = ref<SettingsResponse | null>(null)
const loading = ref(true)
const error = ref('')

const models = ref<ModelInfo[]>([])
const modelsLoading = ref(true)
const modelsError = ref('')
const modelsHint = ref('')

async function loadSettings(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    settings.value = await metaApi.settings()
  } catch (err) {
    error.value = toMessage(err, '加载运行配置失败')
    settings.value = null
  } finally {
    loading.value = false
  }
}

async function loadModels(): Promise<void> {
  modelsLoading.value = true
  modelsError.value = ''
  modelsHint.value = ''
  try {
    const response = await metaApi.models()
    models.value = response.items
    modelsHint.value = response.hint ?? ''
  } catch (err) {
    // 没有可用账号时后端返回 503，这是部署初期的正常状态，
    // 所以要给一句能指向解决办法的话，而不是一个红色报错。
    modelsError.value = toMessage(err, '暂时无法获取模型列表')
    models.value = []
  } finally {
    modelsLoading.value = false
  }
}

onMounted(() => {
  void loadSettings()
  void loadModels()
})

const upstreamRows = computed<SettingRow[]>(() => {
  const upstream = settings.value?.upstream
  if (!upstream) return []
  return [
    { label: '上游地址', value: upstream.base_url, mono: true },
    { label: '项目 Slug', value: upstream.project_slug, mono: true, hint: '以 x-project-slug 头发送' },
    { label: 'CLI 版本', value: upstream.cli_version, mono: true, hint: '随请求上报，用于伪装成官方 CLI' },
    {
      label: '指纹盐',
      value: upstream.fingerprint_salt_set ? upstream.fingerprint_salt_masked : '未设置',
      mono: true,
      tone: upstream.fingerprint_salt_set ? 'success' : 'warning',
      hint: '参与设备指纹派生，更换会让所有账号一起换设备',
    },
    {
      label: '空 system 占位',
      value: upstream.empty_system_placeholder ? '启用' : '停用',
      tone: upstream.empty_system_placeholder ? 'success' : 'neutral',
      hint: '请求没有 system 提示词时塞一个空格，防止上游注入默认提示词',
    },
    { label: '流式空闲超时', value: upstream.stream_idle_timeout, mono: true },
    { label: '非流式空闲超时', value: upstream.nonstream_idle_timeout, mono: true },
    { label: '请求体上限', value: `${upstream.max_body_mb} MB` },
    { label: '模型缓存时长', value: upstream.model_cache_ttl, mono: true },
  ]
})

const authRows = computed<SettingRow[]>(() => {
  const auth = settings.value?.auth
  if (!auth) return []
  return [
    { label: '登录令牌有效期', value: auth.token_ttl, mono: true, hint: '过期后需要重新登录' },
    { label: '登录模式', value: 'JWT Bearer（仅管理员）' },
  ]
})

const healthRows = computed<SettingRow[]>(() => {
  const health = settings.value?.health_check
  if (!health) return []
  return [
    {
      label: '健康检查',
      value: health.enabled ? '启用' : '停用',
      tone: health.enabled ? 'success' : 'warning',
      hint: '定时探测账号可用性，连续失败会自动禁用账号',
    },
    { label: '探测周期', value: health.interval, mono: true },
    { label: '失败阈值', value: `${health.failure_threshold} 次`, hint: '连续失败达到该次数后自动禁用' },
    { label: '单次超时', value: health.timeout, mono: true },
  ]
})
</script>

<template>
  <div class="space-y-4">
    <!-- 配置是只读的，这一点必须先说清楚，否则会有人到处找保存按钮 -->
    <div class="flex flex-wrap items-center gap-2 rounded-panel border border-info/25 bg-info/10 px-3 py-2.5 text-2xs text-info">
      <Icon name="info" :size="14" />
      <span>
        这里展示的是服务当前生效的参数，均为只读。修改需要调整后端环境变量并重启服务——
        配置来源只有环境变量一处，不会出现「界面改了但没生效」。
      </span>
    </div>

    <p
      v-if="error"
      class="flex items-center gap-2 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-2xs text-danger"
    >
      <Icon name="alertCircle" :size="13" />
      {{ error }}
      <Button variant="ghost" size="xs" class="ml-auto" @click="loadSettings">重试</Button>
    </p>

    <div class="grid gap-4 lg:grid-cols-2">
      <Card title="上游接入" subtitle="Command Code 的接入参数" body-class="px-4 py-2">
        <div v-if="loading" class="space-y-3 py-2">
          <Skeleton v-for="i in 6" :key="i" class="h-4" />
        </div>
        <dl v-else class="divide-y divide-line">
          <div
            v-for="row in upstreamRows"
            :key="row.label"
            class="flex items-start justify-between gap-4 py-2"
          >
            <dt class="min-w-0">
              <span class="text-2xs text-muted">{{ row.label }}</span>
              <span v-if="row.hint" class="mt-0.5 block text-2xs text-subtle">{{ row.hint }}</span>
            </dt>
            <dd class="shrink-0 text-right">
              <Badge v-if="row.tone" :tone="row.tone">{{ row.value }}</Badge>
              <span v-else class="text-[13px] text-fg" :class="row.mono ? 'font-mono text-2xs' : ''">
                {{ row.value }}
              </span>
            </dd>
          </div>
        </dl>
      </Card>

      <div class="space-y-4">
        <Card title="认证" subtitle="管理后台登录" body-class="px-4 py-2">
          <div v-if="loading" class="space-y-3 py-2">
            <Skeleton v-for="i in 2" :key="i" class="h-4" />
          </div>
          <dl v-else class="divide-y divide-line">
            <div
              v-for="row in authRows"
              :key="row.label"
              class="flex items-start justify-between gap-4 py-2"
            >
              <dt class="min-w-0">
                <span class="text-2xs text-muted">{{ row.label }}</span>
                <span v-if="row.hint" class="mt-0.5 block text-2xs text-subtle">{{ row.hint }}</span>
              </dt>
              <dd class="shrink-0 text-right">
                <Badge v-if="row.tone" :tone="row.tone">{{ row.value }}</Badge>
                <span v-else class="text-[13px] text-fg" :class="row.mono ? 'font-mono text-2xs' : ''">
                  {{ row.value }}
                </span>
              </dd>
            </div>
          </dl>
        </Card>

        <Card title="健康检查" subtitle="账号可用性的自动探测" body-class="px-4 py-2">
          <div v-if="loading" class="space-y-3 py-2">
            <Skeleton v-for="i in 4" :key="i" class="h-4" />
          </div>
          <dl v-else class="divide-y divide-line">
            <div
              v-for="row in healthRows"
              :key="row.label"
              class="flex items-start justify-between gap-4 py-2"
            >
              <dt class="min-w-0">
                <span class="text-2xs text-muted">{{ row.label }}</span>
                <span v-if="row.hint" class="mt-0.5 block text-2xs text-subtle">{{ row.hint }}</span>
              </dt>
              <dd class="shrink-0 text-right">
                <Badge v-if="row.tone" :tone="row.tone">{{ row.value }}</Badge>
                <span v-else class="text-[13px] text-fg" :class="row.mono ? 'font-mono text-2xs' : ''">
                  {{ row.value }}
                </span>
              </dd>
            </div>
          </dl>
        </Card>
      </div>
    </div>

    <Card
      title="可用模型"
      :subtitle="`来自上游账号的实时列表，共 ${models.length} 个`"
      body-class="px-4 py-3"
    >
      <template #actions>
        <Button variant="secondary" size="sm" :loading="modelsLoading" @click="loadModels">
          <template #leading><Icon name="refresh" :size="13" /></template>
          重新拉取
        </Button>
      </template>

      <div v-if="modelsLoading" class="flex flex-wrap gap-2">
        <Skeleton v-for="i in 12" :key="i" class="h-6 w-28" rounded="rounded" />
      </div>

      <EmptyState
        v-else-if="modelsError"
        icon="alert"
        tone="warning"
        title="暂时无法获取模型列表"
        :description="`${modelsError}。模型列表需要借一个可用的上游账号向上游查询，请先在「上游账号」页添加账号并测试连接。`"
      />

      <EmptyState
        v-else-if="models.length === 0"
        icon="database"
        tone="warning"
        title="上游没有返回任何模型"
        :description="modelsHint || '确认分组下已有可用账号后重新拉取。'"
      />

      <div v-else class="flex flex-wrap gap-1.5">
        <Badge v-for="model in models" :key="model.id" tone="neutral" mono>
          {{ model.id }}
        </Badge>
      </div>
    </Card>
  </div>
</template>
