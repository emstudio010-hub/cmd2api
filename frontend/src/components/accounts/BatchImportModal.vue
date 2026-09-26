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
import Textarea from '@/components/ui/Textarea.vue'
import { accountsApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { BatchImportResult, Group, UpstreamPlatform } from '@/api/types'
import { useToastStore } from '@/stores/toast'
import { PLATFORMS, accountModeMeta, isValidKey, platformMeta } from '@/utils/platforms'

const props = defineProps<{
  open: boolean
  groups: Group[]
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'imported'): void
}>()

const toast = useToastStore()

/** 整批共用一个平台。默认 Command Code，与历史行为一致。 */
const platform = ref<UpstreamPlatform>('commandcode')
/** 以下两个字段只有 OpenCode 用得到。 */
const accountMode = ref('')
const baseUrl = ref('')
const raw = ref('')
const concurrency = ref('3')
const priority = ref('50')
const groupIds = ref<number[]>([])
const submitting = ref(false)
const serverError = ref('')
const result = ref<BatchImportResult | null>(null)

const meta = computed(() => platformMeta(platform.value))
const usesMode = computed(() => meta.value.usesMode)
/** 当前模式留空 base_url 时会落到的地址，用作占位与提示。 */
const defaultBaseUrl = computed(() => accountModeMeta(accountMode.value).defaultBaseUrl)
const modeError = computed(() =>
  usesMode.value && !accountMode.value ? '请选择计费模式' : '',
)

/** 只列同平台的分组：跨平台的组合后端会直接 400。 */
const platformGroups = computed(() =>
  props.groups.filter((group) => group.platform === platform.value),
)
const hiddenGroupCount = computed(() => props.groups.length - platformGroups.value.length)

interface ParsedLine {
  line: number
  name: string
  key: string
  valid: boolean
}

/**
 * 只看第一个非空白字符：{ 或 [ 按 auth.json 处理，否则当密钥列表。
 *
 * 前端**只做识别，不做解析**。auth.json 的解析在 backend 的 parseKeyLines 里，
 * 一处实现、一处测试——那个解析器有真的边界情况（几个文件首尾相接、字符串里的
 * 花括号），而前端没有测试框架。这里再实现一遍，两边迟早会在某些文件上拆出
 * 不一样的结果，而那种偏差很难被发现。
 */
function looksLikeAuthJson(text: string): boolean {
  const trimmed = text.trimStart()
  return trimmed.startsWith('{') || trimmed.startsWith('[')
}

/**
 * 与后端 parseKeyLines 保持一致的解析规则：
 * 忽略空行与 # 开头的注释；含逗号时按「最后一个逗号」切分「名称,密钥」，
 * 逗号后为空则整行当作密钥。
 *
 * 这份重复是为了让操作员在提交前就看到「哪一行会被丢掉」，而不是提交完
 * 再对着一串错误猜——所以只覆盖规则简单的按行格式。auth.json 那种带边界
 * 情况的解析不在此列，它的逐条结果由提交后的失败列表给出。
 */
const isAuthJson = computed(() => looksLikeAuthJson(raw.value))

const parsed = computed<ParsedLine[]>(() => {
  if (isAuthJson.value) return []

  const out: ParsedLine[] = []
  const lines = raw.value.split('\n')
  lines.forEach((line, index) => {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) return
    let name = ''
    let key = trimmed
    const comma = trimmed.lastIndexOf(',')
    if (comma >= 0) {
      const candidateName = trimmed.slice(0, comma).trim()
      const candidateKey = trimmed.slice(comma + 1).trim()
      if (candidateKey) {
        name = candidateName
        key = candidateKey
      }
    }
    out.push({ line: index + 1, name, key, valid: isValidKey(platform.value, key) })
  })
  return out
})

const validCount = computed(() => parsed.value.filter((item) => item.valid).length)
const invalidCount = computed(() => parsed.value.length - validCount.value)

/** 预览最多渲染 50 行，粘贴几千行时页面不该卡住。 */
const previewLines = computed(() => parsed.value.slice(0, 50))

/**
 * auth.json 的条数只有后端知道，所以它的可提交条件退化成「贴了东西」。
 * 逐条成败由导入结果面板回显。
 */
const canSubmit = computed(() => {
  if (submitting.value || modeError.value) return false
  if (isAuthJson.value) return raw.value.trim().length > 0
  return validCount.value > 0
})

function maskKey(key: string): string {
  if (key.length <= 14) return key
  return `${key.slice(0, 10)}…${key.slice(-4)}`
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    platform.value = 'commandcode'
    accountMode.value = ''
    baseUrl.value = ''
    raw.value = ''
    concurrency.value = '3'
    priority.value = '50'
    groupIds.value = []
    serverError.value = ''
    result.value = null
  },
)

/**
 * 切换平台。OpenCode 专属字段和已选分组在换平台后都不再适用，直接清空——
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

function toggleGroup(id: number, checked: boolean): void {
  if (checked) {
    if (!groupIds.value.includes(id)) groupIds.value = [...groupIds.value, id]
  } else {
    groupIds.value = groupIds.value.filter((item) => item !== id)
  }
}

/**
 * 只提交同平台的分组。分组列表没拉到时（props.groups 为空）不做过滤，
 * 否则会把已勾选的分组悄悄丢掉。
 */
function selectedGroupIds(): number[] {
  if (props.groups.length === 0) return groupIds.value
  const allowed = new Set(platformGroups.value.map((group) => group.id))
  return groupIds.value.filter((id) => allowed.has(id))
}

function toNumber(value: string, fallback: number): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}

async function submit(): Promise<void> {
  if (!canSubmit.value) return
  submitting.value = true
  serverError.value = ''
  try {
    const response = await accountsApi.batchImport({
      // 原样交出去。auth.json 的拆解在后端做，前端不折成「名称,密钥」——
      // 折一遍就等于在前端再实现一次解析，那正是要避免的。
      keys: raw.value,
      platform: platform.value,
      // Command Code 没有这两个字段，显式传空串。
      account_mode: usesMode.value ? accountMode.value : '',
      base_url: usesMode.value ? baseUrl.value.trim() : '',
      concurrency: toNumber(concurrency.value, 3),
      priority: toNumber(priority.value, 50),
      group_ids: selectedGroupIds(),
    })
    result.value = response
    if (response.failed === 0) {
      toast.success(`已导入 ${response.created} 个账号`)
    } else if (response.created === 0) {
      toast.error('全部导入失败', '请检查失败原因后重试')
    } else {
      toast.warning(`导入完成：成功 ${response.created} 个，失败 ${response.failed} 个`)
    }
    if (response.created > 0) emit('imported')
  } catch (err) {
    serverError.value = toMessage(err, '批量导入失败')
  } finally {
    submitting.value = false
  }
}

function close(): void {
  emit('update:open', false)
}
</script>

<template>
  <Modal
    :open="open"
    title="批量导入账号"
    :subtitle="`整批账号共用同一个平台：先选供应商，再粘贴该平台的密钥（${meta.label}）`"
    size="lg"
    @update:open="emit('update:open', $event)"
  >
    <div class="space-y-4">
      <p
        v-if="serverError"
        class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger/10 px-2.5 py-2 text-2xs leading-relaxed text-danger"
      >
        <Icon name="alertCircle" :size="13" class="mt-px" />
        <span>{{ serverError }}</span>
      </p>

      <!-- 平台决定密钥校验规则和可绑定的分组，所以放在最前面 -->
      <Field
        label="上游平台"
        required
        hint="整批共用；不同平台的密钥不能混在一批里导入"
      >
        <PlatformPicker :model-value="platform" @update:model-value="onPlatformChange" />
      </Field>

      <!-- OpenCode 专属：计费模式必填，上游地址留空则由后端按模式回填 -->
      <template v-if="usesMode">
        <Field
          label="计费模式"
          required
          :error="modeError"
          hint="决定计费口径与默认上游地址"
        >
          <AccountModePicker v-model="accountMode" :invalid="Boolean(modeError)" />
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

      <!-- 导入结果。保留在弹窗里而不是直接关掉：操作员需要照着失败行改。 -->
      <div
        v-if="result"
        class="space-y-2 rounded-md border border-line bg-raised/50 p-3"
      >
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-[13px] font-medium text-fg">导入结果</span>
          <span
            class="rounded border border-success/25 bg-success/12 px-1.5 py-px text-2xs text-success"
          >
            成功 {{ result.created }}
          </span>
          <span
            v-if="result.failed > 0"
            class="rounded border border-danger/25 bg-danger/12 px-1.5 py-px text-2xs text-danger"
          >
            失败 {{ result.failed }}
          </span>
        </div>

        <ul
          v-if="result.failures.length > 0"
          class="scroll-thin max-h-40 space-y-1 overflow-y-auto text-2xs"
        >
          <li
            v-for="failure in result.failures"
            :key="`${failure.line}-${failure.key}`"
            class="flex items-start gap-2"
          >
            <span class="tnum shrink-0 text-subtle">第 {{ failure.line }} 行</span>
            <span class="shrink-0 font-mono text-muted">{{ failure.key }}</span>
            <span class="text-danger">{{ failure.error }}</span>
          </li>
        </ul>
      </div>

      <Field
        label="密钥列表"
        hint="每行一条；# 开头的行会被忽略。含逗号时取最后一个逗号切分「名称,密钥」。也可以直接贴 ~/.commandcode/auth.json 的内容，多个账号首尾相接也行"
      >
        <Textarea
          v-model="raw"
          :rows="7"
          mono
          :placeholder="meta.keyPlaceholder"
        />
        <p
          class="mt-1.5 text-2xs"
          :class="meta.keyPrefix ? 'text-warning' : 'text-subtle'"
        >
          {{ meta.keyHint }}
        </p>
      </Field>

      <!--
        贴的是 auth.json 时给个明确回执，否则操作员看不到任何反应，第一反应
        是以为贴错了。这里说不出条数——解析在后端做，前端不预先拆一遍。
      -->
      <p
        v-if="isAuthJson"
        class="flex items-start gap-1.5 rounded-md border border-accent/30 bg-accent/8 px-2.5 py-2 text-2xs leading-relaxed text-accent"
      >
        <Icon name="checkCircle" :size="13" class="mt-px" />
        <span>
          识别为 Command Code 的 auth.json，整段交给后端逐条解析。账号名取自
          文件里的 userName 与 keyName；读不出密钥的条目会单独列在导入结果里。
        </span>
      </p>

      <!--
        预览表只在按行格式下出现：它渲染的是前端那份按行解析的结果，
        auth.json 的解析结果只有后端有。
      -->
      <div v-if="parsed.length > 0" class="flex flex-wrap items-center gap-3 text-2xs">
        <span class="text-subtle">已解析</span>
        <span class="tnum text-fg">{{ parsed.length }} 行</span>
        <span class="tnum text-success">格式正确 {{ validCount }}</span>
        <span v-if="invalidCount > 0" class="tnum text-danger">
          {{ invalidCount }} 行缺少 {{ meta.keyPrefix }} 前缀
        </span>
      </div>

      <div
        v-if="parsed.length > 0"
        class="scroll-thin max-h-44 overflow-y-auto rounded-md border border-line"
      >
        <table class="w-full border-collapse text-left">
          <thead class="sticky top-0 border-b border-line bg-surface">
            <tr>
              <th class="th w-16">行号</th>
              <th class="th">名称</th>
              <th class="th">密钥</th>
              <th class="th w-16">校验</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="item in previewLines" :key="item.line">
              <td class="td tnum text-subtle">{{ item.line }}</td>
              <td class="td">
                {{ item.name || '（自动命名）' }}
              </td>
              <td class="td font-mono text-muted">
                {{ item.key ? maskKey(item.key) : '—' }}
              </td>
              <td class="td">
                <Icon
                  :name="item.valid ? 'checkCircle' : 'alertCircle'"
                  :size="14"
                  :class="item.valid ? 'text-success' : 'text-danger'"
                />
              </td>
            </tr>
          </tbody>
        </table>
        <p
          v-if="parsed.length > previewLines.length"
          class="border-t border-line px-3 py-1.5 text-2xs text-subtle"
        >
          仅预览前 {{ previewLines.length }} 行，其余
          {{ parsed.length - previewLines.length }} 行同样会被导入。
        </p>
      </div>

      <div class="grid gap-3.5 sm:grid-cols-2">
        <Field label="统一并发数" hint="应用到本次导入的所有账号">
          <Input v-model="concurrency" type="number" min="1" step="1" />
        </Field>
        <Field label="统一优先级" hint="数值越小越优先">
          <Input v-model="priority" type="number" min="0" step="1" />
        </Field>
      </div>

      <Field label="统一分组" hint="选中格式错误的行会被后端逐条跳过并列在失败列表里">
        <div
          v-if="platformGroups.length === 0"
          class="space-y-1.5 rounded-md border border-warning/30 bg-warning/10 px-2.5 py-2 text-2xs text-warning"
        >
          <p>
            还没有 {{ meta.label }} 平台的分组，本次导入的账号不会绑定到任何分组，
            从而不会被任何下游密钥使用。
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
          <div class="scroll-thin max-h-32 space-y-2 overflow-y-auto">
            <Checkbox
              v-for="group in platformGroups"
              :key="group.id"
              :model-value="groupIds.includes(group.id)"
              :label="group.name"
              :description="`${group.account_count} 个账号`"
              @update:model-value="toggleGroup(group.id, $event)"
            />
          </div>
          <p v-if="hiddenGroupCount > 0" class="mt-1.5 text-2xs text-subtle">
            另有 {{ hiddenGroupCount }} 个其它平台的分组未列出。
          </p>
        </div>
      </Field>
    </div>

    <template #footer>
      <Button variant="ghost" size="md" :disabled="submitting" @click="close">关闭</Button>
      <Button
        variant="primary"
        size="md"
        :loading="submitting"
        :disabled="!canSubmit"
        @click="submit"
      >
        <!-- auth.json 的条数前端不知道，也就不报数。 -->
        {{ isAuthJson ? '导入账号' : `导入 ${validCount} 个账号` }}
      </Button>
    </template>
  </Modal>
</template>
