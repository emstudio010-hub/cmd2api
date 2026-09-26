<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import AreaChart from '@/components/charts/AreaChart.vue'
import BarList from '@/components/charts/BarList.vue'
import type { BarItem, ChartPoint } from '@/components/charts/types'
import StatCard from '@/components/dashboard/StatCard.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import { dashboardApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { DashboardResponse } from '@/api/types'
import { useTimeRange } from '@/composables/useTimeRange'
import { formatCompact, formatCost, formatDateTime, formatDateTimeShort, formatDuration, formatNumber, formatPercent } from '@/utils/format'

const data = ref<DashboardResponse | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const error = ref('')

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

// Select 的 v-model 是字符串，这里也用字符串而不是字面量联合，
// 免得每次绑定都要断言。
const metric = ref('requests')

const metricOptions = [
  { label: '请求数', value: 'requests' },
  { label: 'Token', value: 'tokens' },
  { label: '费用', value: 'cost' },
]

const rangeLabel = computed(() => {
  if (!data.value) return ''
  return `${formatDateTime(data.value.start)} → ${formatDateTime(data.value.end)}`
})

const bucketLabel = computed(() => (data.value?.runtime.bucket === 'day' ? '按天聚合' : '按小时聚合'))

const hasTraffic = computed(() => (data.value?.summary.requests ?? 0) > 0)

const successTone = computed(() => {
  const rate = data.value?.summary.success_rate ?? 1
  if (rate >= 0.99) return 'success'
  if (rate >= 0.9) return 'warning'
  return 'danger'
})

const chartPoints = computed<ChartPoint[]>(() =>
  (data.value?.series ?? []).map((point) => ({
    label: formatDateTimeShort(point.bucket),
    tooltip: formatDateTime(point.bucket),
    value:
      metric.value === 'requests'
        ? point.requests
        : metric.value === 'cost'
          ? point.total_cost
          : point.input_tokens + point.output_tokens,
  })),
)

const chartFormatter = computed(() => {
  if (metric.value === 'cost') return (value: number) => formatCost(value)
  if (metric.value === 'tokens') return (value: number) => formatCompact(value)
  return (value: number) => formatNumber(value)
})

function toBars(
  rows: { key: string; label: string; requests: number; tokens: number; total_cost: number }[],
): BarItem[] {
  return rows.map((row) => ({
    key: row.key,
    label: row.label,
    value: row.requests,
    secondary: `${formatCompact(row.tokens)} tok · ${formatCost(row.total_cost)}`,
  }))
}

const modelBars = computed(() => toBars(data.value?.models ?? []))
const accountBars = computed(() => toBars(data.value?.accounts ?? []))

async function load(isRefresh = false): Promise<void> {
  if (rangeError.value) return
  if (isRefresh) refreshing.value = true
  else loading.value = true
  error.value = ''
  try {
    const { start, end } = range.value
    data.value = await dashboardApi.fetch(start, end)
  } catch (err) {
    error.value = toMessage(err, '加载概览数据失败')
    data.value = null
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

/** 切回预设并立刻重新拉取——预设下拉框的值可能没变，watch 不会触发。 */
function backToPreset(): void {
  usePreset()
  void load()
}

watch(presetHours, () => {
  if (rangeMode.value === 'preset') void load()
})

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="space-y-4">
    <!-- 工具条：时间范围是这一页最重要的参数，放在最上面且始终可见 -->
    <div class="card flex flex-wrap items-end gap-3 px-3 py-3">
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
        <Button
          v-if="rangeMode === 'custom'"
          variant="subtle"
          size="sm"
          @click="backToPreset"
        >
          返回预设
        </Button>
        <Button v-else variant="ghost" size="sm" @click="useCustom(24)">
          <template #leading><Icon name="clock" :size="13" /></template>
          自定义范围
        </Button>
      </div>

      <template v-if="rangeMode === 'custom'">
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
          <Button variant="primary" size="sm" :disabled="Boolean(rangeError)" @click="load()">
            应用
          </Button>
        </div>
      </template>

      <div class="ml-auto flex items-center gap-2 pb-1">
        <span v-if="rangeLabel" class="hidden text-2xs text-subtle lg:block">{{ rangeLabel }}</span>
        <Button variant="secondary" size="sm" :loading="refreshing" @click="load(true)">
          <template #leading><Icon name="refresh" :size="13" /></template>
          刷新
        </Button>
      </div>
    </div>

    <!-- 加载失败 -->
    <Card v-if="error" body-class="p-0">
      <EmptyState
        icon="alertCircle"
        tone="danger"
        title="概览数据加载失败"
        :description="error"
      >
        <Button variant="primary" size="sm" @click="load()">重试</Button>
      </EmptyState>
    </Card>

    <template v-else>
      <!-- 运行状态 -->
      <div class="flex flex-wrap items-center gap-2">
        <template v-if="loading">
          <Skeleton class="h-6 w-28" rounded="rounded" />
          <Skeleton class="h-6 w-24" rounded="rounded" />
          <Skeleton class="h-6 w-24" rounded="rounded" />
        </template>
        <template v-else-if="data">
          <Badge tone="accent" dot>
            账号 {{ data.runtime.accounts_active }} / {{ data.runtime.accounts_total }} 可用
          </Badge>
          <Badge tone="neutral" dot>下游密钥 {{ data.runtime.api_keys_total }}</Badge>
          <Badge tone="info" dot>{{ bucketLabel }}</Badge>
        </template>
      </div>

      <!-- 概览数字 -->
      <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard
          label="请求数"
          icon="activity"
          tone="accent"
          :loading="loading"
          :value="formatNumber(data?.summary.requests ?? 0)"
          :hint="`失败 ${formatNumber(data?.summary.failed ?? 0)} 次`"
        />
        <StatCard
          label="成功率"
          icon="checkCircle"
          :tone="successTone"
          :loading="loading"
          :value="formatPercent(data?.summary.success_rate ?? 0, 2)"
          :hint="`基于 ${formatNumber(data?.summary.requests ?? 0)} 次请求`"
        />
        <StatCard
          label="总费用"
          icon="zap"
          tone="info"
          :loading="loading"
          :value="formatCost(data?.summary.total_cost ?? 0)"
          hint="按账号倍率折算后"
        />
        <StatCard
          label="平均耗时"
          icon="clock"
          :loading="loading"
          :value="formatDuration(data?.summary.avg_duration_ms ?? 0)"
          :hint="`首 token ${formatDuration(data?.summary.avg_first_token_ms ?? 0)}`"
        />
        <StatCard
          label="输入 Token"
          icon="database"
          :loading="loading"
          :value="formatCompact(data?.summary.input_tokens ?? 0)"
          hint="上行"
        />
        <StatCard
          label="输出 Token"
          icon="database"
          :loading="loading"
          :value="formatCompact(data?.summary.output_tokens ?? 0)"
          hint="下行"
        />
        <StatCard
          label="缓存读取 Token"
          icon="database"
          :loading="loading"
          :value="formatCompact(data?.summary.cache_read_tokens ?? 0)"
          hint="命中缓存的输入"
        />
        <StatCard
          label="平均首 Token"
          icon="clock"
          :loading="loading"
          :value="formatDuration(data?.summary.avg_first_token_ms ?? 0)"
          hint="仅统计流式请求"
        />
      </div>

      <!-- 时间序列 -->
      <Card title="请求趋势" :subtitle="rangeLabel" body-class="px-3 pb-3 pt-2">
        <template #actions>
          <div class="w-[110px]">
            <Select v-model="metric" size="sm" :options="metricOptions" />
          </div>
        </template>

        <div v-if="loading" class="flex h-[220px] items-end gap-1 px-1">
          <Skeleton v-for="i in 24" :key="i" class="flex-1" :style="{ height: `${20 + ((i * 37) % 70)}%` }" />
        </div>
        <AreaChart
          v-else
          :points="chartPoints"
          :format-value="chartFormatter"
          :height="240"
          :empty-text="hasTraffic ? '该范围内没有时间序列数据' : '所选时间范围内没有任何请求'"
        />
      </Card>

      <!-- 分项排行 -->
      <div class="grid gap-4 lg:grid-cols-2">
        <Card title="按模型" subtitle="按请求数排序，最多 10 项">
          <div v-if="loading" class="space-y-3">
            <Skeleton v-for="i in 5" :key="i" class="h-6" />
          </div>
          <BarList v-else :items="modelBars" empty-text="该范围内没有模型调用记录" />
        </Card>

        <Card title="按上游账号" subtitle="按请求数排序，最多 10 项">
          <div v-if="loading" class="space-y-3">
            <Skeleton v-for="i in 5" :key="i" class="h-6" />
          </div>
          <BarList
            v-else
            :items="accountBars"
            tone="success"
            empty-text="该范围内没有账号调用记录"
          />
        </Card>
      </div>

      <Card v-if="!loading && !hasTraffic" body-class="p-0">
        <EmptyState
          icon="activity"
          title="所选时间范围内没有请求"
          description="如果刚部署完还没接入客户端，请先在上游账号页添加 Command Code 账号，再签发一把下游密钥。"
        >
          <RouterLink :to="{ name: 'accounts' }">
            <Button variant="primary" size="sm">去添加账号</Button>
          </RouterLink>
        </EmptyState>
      </Card>
    </template>
  </div>
</template>
