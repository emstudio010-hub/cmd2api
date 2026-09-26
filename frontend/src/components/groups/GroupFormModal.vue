<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Button from '@/components/ui/Button.vue'
import Field from '@/components/ui/Field.vue'
import Icon from '@/components/ui/Icon.vue'
import Input from '@/components/ui/Input.vue'
import Modal from '@/components/ui/Modal.vue'
import Select from '@/components/ui/Select.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { groupsApi } from '@/api/endpoints'
import { toMessage } from '@/api/client'
import type { Group, GroupPayload } from '@/api/types'
import { useToastStore } from '@/stores/toast'

const props = defineProps<{
  open: boolean
  /** null 表示新建。 */
  group: Group | null
}>()

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'saved'): void
}>()

const toast = useToastStore()

const isEdit = computed(() => props.group !== null)

const name = ref('')
const description = ref('')
const multiplier = ref('1')
const status = ref('active')
const submitting = ref(false)
const serverError = ref('')

const statusOptions = [
  { label: '启用', value: 'active' },
  { label: '禁用', value: 'disabled' },
]

const errors = computed(() => {
  const out: Record<string, string> = {}
  if (!name.value.trim()) out.name = '请输入分组名称'
  const parsed = Number(multiplier.value)
  if (!Number.isFinite(parsed) || parsed <= 0) out.multiplier = '倍率必须大于 0'
  return out
})

const valid = computed(() => Object.keys(errors.value).length === 0)

watch(
  () => [props.open, props.group] as const,
  () => {
    if (!props.open) return
    name.value = props.group?.name ?? ''
    description.value = props.group?.description ?? ''
    multiplier.value = String(props.group?.rate_multiplier ?? 1)
    status.value = props.group?.status ?? 'active'
    serverError.value = ''
  },
  { immediate: true },
)

function close(): void {
  emit('update:open', false)
}

async function submit(): Promise<void> {
  if (!valid.value || submitting.value) return
  submitting.value = true
  serverError.value = ''
  try {
    const payload: GroupPayload = {
      name: name.value.trim(),
      description: description.value,
      rate_multiplier: Number(multiplier.value),
      status: status.value,
    }
    if (isEdit.value && props.group) {
      await groupsApi.update(props.group.id, payload)
      toast.success('分组已更新', payload.name)
    } else {
      await groupsApi.create(payload)
      toast.success('分组已创建', payload.name)
    }
    emit('saved')
    close()
  } catch (err) {
    serverError.value = toMessage(err, isEdit.value ? '更新分组失败' : '创建分组失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal
    :open="open"
    :title="isEdit ? '编辑分组' : '新建分组'"
    subtitle="分组决定下游密钥能用哪些账号，以及计费倍率"
    size="md"
    @update:open="emit('update:open', $event)"
  >
    <form class="space-y-3.5" @submit.prevent="submit">
      <p
        v-if="serverError"
        class="flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger/10 px-2.5 py-2 text-2xs leading-relaxed text-danger"
      >
        <Icon name="alertCircle" :size="13" class="mt-px" />
        <span>{{ serverError }}</span>
      </p>

      <Field label="分组名称" required :error="errors.name">
        <Input v-model="name" placeholder="例如：默认分组 / 内部测试" maxlength="100" />
      </Field>

      <Field label="描述" hint="选填，说明这个分组给谁用">
        <Textarea v-model="description" :rows="2" placeholder="可选" />
      </Field>

      <div class="grid gap-3.5 sm:grid-cols-2">
        <Field
          label="费用倍率"
          :error="errors.multiplier"
          hint="在账号倍率之上再乘一次"
        >
          <Input v-model="multiplier" type="number" min="0" step="0.01" />
        </Field>

        <Field label="状态" hint="禁用后该分组下的密钥不再可用">
          <Select v-model="status" :options="statusOptions" />
        </Field>
      </div>
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
        {{ isEdit ? '保存修改' : '创建分组' }}
      </Button>
    </template>
  </Modal>
</template>
