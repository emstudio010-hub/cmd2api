<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
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
  StartAccountOAuthPayload,
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
const oauthLoading = ref(false)

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

/**
 * 浏览器授权这条路不校验密钥——那正是它要去拿的东西。
 * 其余字段的校验跟手动填一样，因为它们要一起存到后端等回调时建号。
 */
const validWithoutKey = computed(() =>
  Object.keys(errors.value).every((field) => field === 'apiKey'),
)

/**
 * 只有新建 Command Code 账号时能给浏览器授权入口。
 *
 * 编辑态不给：换密钥是另一回事，而且平台不可改。OpenCode 也不给：
 * 上游没有对应的授权流程，硬放一个按钮只会让人以为它坏了。
 */
const canStartOAuth = computed(() => !isEdit.value && platform.value === 'commandcode')

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
  // 关掉弹窗等于放弃这次等待：轮询必须停，否则它会一直往后端打，
  // 直到组件被销毁。
  stopOAuthPolling()
  oauthPhase.value = 'idle'
  emit('update:open', false)
}

/**
 * 浏览器授权的三个阶段。
 *
 * idle → waiting：点了「用浏览器登录」，已经在新标签页打开了授权页。
 * waiting 期间这个弹窗一直留着，用户填了一半的表单也还在。
 *
 * 收尾有两条路，两条都会把账号建出来：
 *   - 自动：studio 把浏览器跳回后端回调地址，后端建完号，另一个标签页落回
 *     账号页；这个页面轮询到结果后自己收工。
 *   - 手动：浏览器跳不回本机（面板在远程服务器上）时，用户把地址栏里那条
 *     打不开的地址粘进来。这时回调从没被访问过，state 还挂着，所以后端能
 *     用粘回来的 state 认出是哪次握手、沿用上面填的那些字段。
 */
type OAuthPhase = 'idle' | 'waiting' | 'copyback'

const oauthPhase = ref<OAuthPhase>('idle')
const oauthState = ref('')
const oauthCallbackUrl = ref('')
/**
 * 回调地址是不是指向本机。
 *
 * studio 只接受 localhost 的回调地址（它自己的授权页上写着 "Only localhost
 * URLs are allowed for security"，判不过就整页换成 Invalid Request）。面板装在
 * 远程服务器上时回调必然是远程域名，所以后端会改成递一个**没人监听的**
 * localhost 地址进去，让 studio 那一下打空、退回到它自己的「复制密钥」页。
 *
 * 这个标志因此决定的是收尾方式，不是「能不能用」：
 * 真 → 自动跳回来；假 → 用户从 studio 页面上把密钥抄回来。
 */
const oauthCallbackIsLoopback = ref(true)
/**
 * 这次握手要打开的授权页地址。
 *
 * 留着是为了弹窗被拦时能摆一条用户自己点的链接——总得给个出口，但不用
 * 拿本页跳转去换（那会连表单一起弄丢）。
 */
const oauthAuthUrl = ref('')
/** 新标签页没开出来（被弹窗拦截拦了）。这时才需要那条手动链接。 */
const oauthTabBlocked = ref(false)
const oauthError = ref('')
/** 手动粘贴的内容：一条回调地址，或者一把裸密钥。 */
const oauthPaste = ref('')
const oauthPasting = ref(false)
const oauthCopied = ref(false)

/** 轮询定时器。onBeforeUnmount 和手动收工都要清掉，不然会一直往后端打。 */
let oauthTimer: ReturnType<typeof setInterval> | null = null

function stopOAuthPolling(): void {
  if (oauthTimer !== null) {
    clearInterval(oauthTimer)
    oauthTimer = null
  }
}

onBeforeUnmount(stopOAuthPolling)

/** 把授权表单字段打包，自动和手动两条路共用。 */
function oauthFields(): StartAccountOAuthPayload {
  const expires = clearExpiry.value ? '' : fromLocalInputValue(expiresAt.value) || undefined
  return {
    name: name.value.trim(),
    notes: notes.value,
    concurrency: toNumber(concurrency.value),
    priority: toNumber(priority.value),
    rate_multiplier: toNumber(multiplier.value),
    group_ids: selectedGroupIds(),
    ...(expires ? { expires_at: expires } : {}),
  }
}

/** 两条路收尾后要做的事完全一样。 */
function finishOAuth(accountName: string): void {
  stopOAuthPolling()
  oauthPhase.value = 'idle'
  toast.success(
    accountName ? `账号「${accountName}」已创建` : '账号已创建',
    '已完成浏览器授权',
  )
  emit('saved')
  close()
}

/**
 * 用浏览器登录代替手贴密钥。
 *
 * **在新标签页里打开**，不是整页跳走。以前是整页跳走，代价是丢了当前页面
 * 的一切：表单填了一半的内容、滚动位置，回来还得重新找。开新标签页之后，
 * 这个页面靠轮询知道自己该收工了——这也正是 oauthPhase 存在的理由。
 *
 * 弹窗真被拦下时**不**跳转本页（见 openAuthTab）：本页一跳，用户填了一半的
 * 表单就没了，而这条链路本来就有别的办法收尾。改成在面板里摆一条能点的链接，
 * 让用户自己决定。
 */
async function startOAuth(): Promise<void> {
  if (!validWithoutKey.value || oauthLoading.value) return
  if (!platform.value) return

  oauthLoading.value = true
  serverError.value = ''
  oauthError.value = ''
  try {
    const result = await accountsApi.startOAuth(oauthFields())
    oauthState.value = result.state
    oauthCallbackUrl.value = result.callback_url
    oauthAuthUrl.value = result.auth_url
    oauthCallbackIsLoopback.value = result.callback_is_loopback !== false
    oauthPaste.value = ''

    oauthTabBlocked.value = !openAuthTab(result.auth_url)
    // 面板不在本机时是「抄回来」而不是「跳回来」：studio 那一下 POST 打到
    // 用户自己机器的回环地址上，我们收不到，它会退回到自己那页把密钥显示
    // 出来让用户复制。这种情况不能轮询——那条回调永远不会到。
    oauthPhase.value = oauthCallbackIsLoopback.value ? 'waiting' : 'copyback'
    if (oauthCallbackIsLoopback.value) {
      startOAuthPolling()
    }
  } catch (err) {
    serverError.value = toMessage(err, '发起浏览器授权失败')
  } finally {
    oauthLoading.value = false
  }
}

/**
 * 在新标签页里打开授权页，返回「是不是真开出来了」。
 *
 * **不要在 features 里写 noopener。** 按规范，带上 noopener 的 window.open
 * 一律返回 null——开的照开，就是不给你 handle。而 null 和「弹窗被拦了」长得
 * 一模一样，于是调用方每次都会误判成被拦，去走那条整页跳转的兜底：结果是
 * 新标签页开了、当前页面也跳走了，用户填了一半的表单跟着没。这是实测撞到的。
 *
 * 该有的隔离换个写法：拿到 handle 之后**立刻**掐断 opener，效果和 noopener
 * 一样，但 handle 还在，能分清到底开没开成。
 */
function openAuthTab(url: string): boolean {
  const tab = window.open(url, '_blank')
  if (!tab) return false
  try {
    tab.opener = null
  } catch {
    // 掐不断也认了：那只是让目标页能反过来操作本页，不影响这趟授权能不能走完。
  }
  return true
}

/**
 * 每两秒问一次「好了没」。
 *
 * 两秒是权衡的结果：快了是在对一个十分钟的窗口浪费请求，慢了用户会觉得
 * 卡。请求本身很轻（一次内存查表）。
 */
function startOAuthPolling(): void {
  stopOAuthPolling()
  oauthTimer = setInterval(() => {
    void pollOAuth()
  }, 2000)
}

async function pollOAuth(): Promise<void> {
  const state = oauthState.value
  if (!state) return
  try {
    const result = await accountsApi.oauthStatus(state)
    if (result.status === 'ok') {
      finishOAuth(result.name)
      return
    }
    if (result.status === 'error') {
      // 失败就停止轮询：再等下去也是同一个答案。弹窗留着，用户可以直接
      // 用手动那条路把这次授权救回来。
      stopOAuthPolling()
      oauthError.value = result.message
      return
    }
    if (result.status === 'gone') {
      stopOAuthPolling()
      oauthError.value = result.message
    }
    // pending：继续等。
  } catch {
    // 轮询失败不打扰用户：可能只是一次网络抖动，下一次还会问。
    // 真正卡住（比如登录过期）时，手动粘贴那条路仍然可用。
  }
}

/**
 * 手动收尾。
 *
 * 用在浏览器跳不回本机的情形：面板跑在远程服务器上，studio 让浏览器跳回
 * 「跑着 cmd2api 的那台机器的回调地址」，浏览器打不开，页面就停在那儿——
 * 而密钥正好在那条打不开的地址里。让用户把地址栏内容复制回来，这趟授权
 * 就不算白做。
 *
 * 也直接接受一把裸密钥：很多人第一反应就是只复制密钥本身。
 */
async function completeOAuthManually(): Promise<void> {
  const result = oauthPaste.value.trim()
  if (!result || oauthPasting.value) return

  oauthPasting.value = true
  oauthError.value = ''
  try {
    const account = await accountsApi.completeOAuth({
      result,
      // 带上 state，后端就能沿用发起时存在那份表单字段（名称、分组、
      // 优先级……）。粘回来的地址里其实也带 state，这是多一层保险。
      ...(oauthState.value ? { state: oauthState.value } : {}),
      ...oauthFields(),
    })
    finishOAuth(account.name)
  } catch (err) {
    oauthError.value = toMessage(err, '手动完成授权失败')
  } finally {
    oauthPasting.value = false
  }
}

async function copyCallbackUrl(): Promise<void> {
  if (!oauthCallbackUrl.value) return
  try {
    await navigator.clipboard.writeText(oauthCallbackUrl.value)
    oauthCopied.value = true
    setTimeout(() => {
      oauthCopied.value = false
    }, 1500)
  } catch {
    toast.error('复制失败', '请手动选中下面的地址复制')
  }
}

/** 放弃这次等待。不撤销后端的握手——它十分钟后自己过期。 */
function cancelOAuth(): void {
  stopOAuthPolling()
  oauthPhase.value = 'idle'
  oauthError.value = ''
  oauthPaste.value = ''
  oauthAuthUrl.value = ''
  oauthTabBlocked.value = false
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

          <!--
            浏览器授权是手贴密钥之外的另一条路，不是替代：两条路拿到的
            都是同一种 user_ 密钥，谁方便用谁。
          -->
          <template v-if="canStartOAuth">
            <!-- 还没开始，或者放弃了等待 -->
            <div v-if="oauthPhase === 'idle'" class="mt-2 flex flex-wrap items-center gap-2">
              <Button
                variant="subtle"
                size="sm"
                :loading="oauthLoading"
                :disabled="!validWithoutKey"
                @click="startOAuth"
              >
                <Icon name="external" :size="13" />
                用浏览器登录
              </Button>
              <span class="text-2xs text-subtle">
                在新标签页里登录授权，这一页不会跳走
              </span>
            </div>

            <!--
              面板不在本机：收尾方式从「跳回来」变成「抄回来」。

              后端已经把回调地址换成了一个没人监听的 localhost 地址，studio
              因此会放行（它只认 localhost），登录、选账号、授权这些步骤一步
              不少；只是最后那一下 POST 打在用户自己机器的空端口上，打不通，
              studio 于是跳到它自己的「Copy your API key」页把密钥显示出来。
              用户复制，粘回下面这个框，走的是同一条 /complete。
            -->
            <div
              v-else-if="oauthPhase === 'copyback'"
              class="mt-2 space-y-2.5 rounded-md border border-line bg-raised px-2.5 py-2.5"
            >
              <div class="flex items-start gap-2">
                <Icon name="external" :size="13" class="mt-0.5 shrink-0 text-accent" />
                <div class="min-w-0 flex-1">
                  <p class="text-2xs font-medium text-fg">
                    {{ oauthTabBlocked ? '登录页没能自动打开' : '已在新标签页打开登录页' }}
                  </p>
                  <p class="mt-0.5 text-2xs leading-relaxed text-subtle">
                    面板不在你本机上，Command Code 没法把结果直接交回来
                    （它的授权页只接受
                    <code class="font-mono">localhost</code> 的回调）。所以最后
                    一步得你搭把手：
                  </p>
                  <p v-if="oauthTabBlocked" class="mt-1.5 text-2xs leading-relaxed text-subtle">
                    浏览器拦下了新标签页，点
                    <a
                      :href="oauthAuthUrl"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="text-accent underline underline-offset-2"
                    >这里手动打开登录页</a
                    >。这一页不会跳走，你填的东西都还在。
                  </p>
                </div>
              </div>

              <ol class="ml-4 list-decimal space-y-1.5 text-2xs leading-relaxed text-subtle">
                <li v-if="!oauthTabBlocked">在新标签页里登录并授权。</li>
                <li v-else>在上面那条链接里登录并授权。</li>
                <li>
                  完事后 Command Code 会显示一页
                  <strong class="font-medium text-muted">「Copy your API key」</strong>，
                  点上面的复制按钮。
                </li>
                <li>把它粘到下面，点「完成」。粘整页内容也行，这里只要那串密钥。</li>
              </ol>

              <div class="space-y-2">
                <Textarea
                  v-model="oauthPaste"
                  :rows="2"
                  placeholder="粘贴 Copy your API key 页面上的那串密钥"
                  @keydown.enter.exact.prevent="completeOAuthManually"
                />
                <div class="flex items-center gap-2">
                  <Button
                    size="sm"
                    :loading="oauthPasting"
                    :disabled="!oauthPaste.trim()"
                    @click="completeOAuthManually"
                  >
                    完成
                  </Button>
                  <Button variant="ghost" size="sm" @click="cancelOAuth">取消</Button>
                </div>
              </div>

              <p v-if="oauthError" class="text-2xs leading-relaxed text-danger">
                {{ oauthError }}
              </p>

              <p class="text-2xs leading-relaxed text-subtle">
                嫌这一步麻烦的话：在你自己电脑上开一条 SSH 隧道，用
                <code class="font-mono">http://127.0.0.1:8080</code>
                打开面板，这条链路就会自己走通，不用复制粘贴。本地 8080 被占着
                就换个端口，端口是几都行。
              </p>
            </div>

            <!-- 等另一个标签页里那次授权的结果 -->
            <div
              v-else
              class="mt-2 space-y-2.5 rounded-md border border-line bg-raised px-2.5 py-2.5"
            >
              <div class="flex items-start gap-2">
                <Icon
                  v-if="!oauthError"
                  name="refresh"
                  :size="13"
                  class="mt-0.5 shrink-0 animate-spin text-accent"
                />
                <Icon v-else name="alertCircle" :size="13" class="mt-0.5 shrink-0 text-danger" />
                <div class="min-w-0 flex-1">
                  <p class="text-2xs font-medium text-fg">
                    {{
                      oauthError
                        ? '这次授权没成'
                        : oauthTabBlocked
                          ? '登录页没能自动打开'
                          : '已在新标签页打开登录页'
                    }}
                  </p>
                  <p class="mt-0.5 text-2xs leading-relaxed text-subtle">
                    <template v-if="oauthError">{{ oauthError }}</template>
                    <template v-else-if="oauthTabBlocked">
                      浏览器拦下了新标签页，点
                      <a
                        :href="oauthAuthUrl"
                        target="_blank"
                        rel="noopener noreferrer"
                        class="text-accent underline underline-offset-2"
                      >这里手动打开登录页</a
                      >。这一页不会跳走，你填的东西都还在。
                    </template>
                    <template v-else>
                      登录完成后这个窗口会自己发现并建号，不用手动刷新。
                    </template>
                  </p>
                </div>
              </div>

              <!--
                手动那条路。默认折叠着：正常情况下用不上，摊开只会让人以为要填。

                什么时候用得上：自动那一步没走完（比如中途关了标签页、或者
                studio 那边 fetch 超时），但密钥已经在屏幕上显示出来了。
              -->
              <details class="group">
                <summary
                  class="cursor-pointer list-none text-2xs text-muted transition hover:text-fg"
                >
                  <span class="inline-flex items-center gap-1">
                    <Icon name="chevronRight" :size="12" class="group-open:rotate-90" />
                    密钥已经显示出来了？
                  </span>
                </summary>

                <div class="mt-2 space-y-2">
                  <p class="text-2xs leading-relaxed text-subtle">
                    把 Command Code 那页「Copy your API key」上的密钥粘到这里。
                    整段文字粘进来也行——这里只认其中那串
                    <code class="font-mono">user_</code> 密钥。粘进来会先拿去
                    校验再建号，校验不过不会留下半个账号。
                  </p>
                  <Textarea
                    v-model="oauthPaste"
                    :rows="2"
                    :mono="true"
                    placeholder="粘贴 user_ 开头的那串密钥"
                  />
                  <div class="flex flex-wrap items-center gap-2">
                    <Button
                      variant="subtle"
                      size="sm"
                      :loading="oauthPasting"
                      :disabled="!oauthPaste.trim()"
                      @click="completeOAuthManually"
                    >
                      用这段内容完成
                    </Button>
                  </div>
                </div>
              </details>

              <!--
                回调地址本身。放在「手动」之后、折叠起来：它主要是排错用的
                （反代把 Host 改坏了的时候，一眼能看出拼成了什么）。
              -->
              <details class="group">
                <summary
                  class="cursor-pointer list-none text-2xs text-muted transition hover:text-fg"
                >
                  <span class="inline-flex items-center gap-1">
                    <Icon name="chevronRight" :size="12" class="group-open:rotate-90" />
                    查看本次的回调地址
                  </span>
                </summary>
                <div class="mt-2 space-y-2">
                  <p class="break-all font-mono text-2xs text-subtle">
                    {{ oauthCallbackUrl }}
                  </p>
                  <Button variant="ghost" size="sm" @click="copyCallbackUrl">
                    <Icon :name="oauthCopied ? 'check' : 'copy'" :size="12" />
                    {{ oauthCopied ? '已复制' : '复制' }}
                  </Button>
                </div>
              </details>

              <div class="flex justify-end">
                <Button variant="ghost" size="sm" @click="cancelOAuth">停止等待</Button>
              </div>
            </div>
          </template>
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
