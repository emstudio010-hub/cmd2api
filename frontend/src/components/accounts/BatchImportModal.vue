<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Modal from '@/components/ui/Modal.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { accountsApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { BatchImportResult, Group } from '@/api/types'
import { useToastStore } from '@/stores/toast'

const props = defineProps<{
  open: boolean
  groups: Group[]
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'imported'): void
}>()

const toast = useToastStore()

const raw = ref('')
const concurrency = ref('3')
const priority = ref('50')
const groupIds = ref<number[]>([])
const submitting = ref(false)
const serverError = ref('')
const result = ref<BatchImportResult | null>(null)

interface ParsedLine {
  line: number
  name: string
  key: string
  valid: boolean
}

/**
 * 与后端 parseKeyLines 保持一致的解析规则：
 * 忽略空行与 # 开头的注释；含逗号时按「最后一个逗号」切分「名称,密钥」，
 * 逗号后为空则整行当作密钥。
 *
 * 前端先解析一遍是为了让操作员在提交前就看到「哪一行会被丢掉」，
 * 而不是提交完再对着一串错误猜。
 */
const parsed = computed<ParsedLine[]>(() => {
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
    out.push({ line: index + 1, name, key, valid: key.startsWith('user_') })
  })
  return out
})

const validCount = computed(() => parsed.value.filter((item) => item.valid).length)
const invalidCount = computed(() => parsed.value.length - validCount.value)

/** 预览最多渲染 50 行，粘贴几千行时页面不该卡住。 */
const previewLines = computed(() => parsed.value.slice(0, 50))

const canSubmit = computed(() => validCount.value > 0 && !submitting.value)

function maskKey(key: string): string {
  if (key.length <= 14) return key
  return `${key.slice(0, 10)}…${key.slice(-4)}`
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    raw.value = ''
    concurrency.value = '3'
    priority.value = '50'
    groupIds.value = []
    serverError.value = ''
    result.value = null
  },
)

function toggleGroup(id: number, checked: boolean): void {
  if (checked) {
    if (!groupIds.value.includes(id)) groupIds.value = [...groupIds.value, id]
  } else {
    groupIds.value = groupIds.value.filter((item) => item !== id)
  }
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
      keys: raw.value,
      concurrency: toNumber(concurrency.value, 3),
      priority: toNumber(priority.value, 50),
      group_ids: groupIds.value,
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
    subtitle="每行一条，支持「密钥」或「名称,密钥」两种写法"
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
        hint="每行一条；# 开头的行会被忽略。含逗号时取最后一个逗号切分「名称,密钥」"
      >
        <Textarea
          v-model="raw"
          :rows="8"
          mono
          placeholder="user_xxxxxxxxxxxxxxxx"
        />
      </Field>

      <div class="flex flex-wrap items-center gap-3 text-2xs">
        <span class="text-subtle">已解析</span>
        <span class="tnum text-fg">{{ parsed.length }} 行</span>
        <span class="tnum text-success">格式正确 {{ validCount }}</span>
        <span v-if="invalidCount > 0" class="tnum text-danger">
          {{ invalidCount }} 行缺少 user_ 前缀
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
              <td class="td font-mono text-muted">{{ maskKey(item.key) }}</td>
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
          v-if="groups.length === 0"
          class="rounded-md border border-warning/30 bg-warning/10 px-2.5 py-2 text-2xs text-warning"
        >
          还没有任何分组，导入的账号不会绑定到任何分组。
        </div>
        <div
          v-else
          class="scroll-thin max-h-32 space-y-2 overflow-y-auto rounded-md border border-line bg-raised/40 p-2.5"
        >
          <Checkbox
            v-for="group in groups"
            :key="group.id"
            :model-value="groupIds.includes(group.id)"
            :label="group.name"
            :description="`${group.account_count} 个账号`"
            @update:model-value="toggleGroup(group.id, $event)"
          />
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
        导入 {{ validCount }} 个账号
      </Button>
    </template>
  </Modal>
</template>
