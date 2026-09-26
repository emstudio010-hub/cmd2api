// 上游平台的展示元数据。
//
// 两个平台在表单形态、密钥校验规则、能绑定哪些分组上都不一样，把这些差异
// 集中在这一处，免得账号弹窗、批量导入、分组弹窗各写一份还写歪——
// 「Command Code 要 user_ 前缀、OpenCode 不要」这种规则只该有一个出处。

import type { AccountMode, UpstreamPlatform } from '@/api/types'

/** 平台徽标的配色，取值来自 ui/Badge 的 tone。 */
type PlatformTone = 'accent' | 'info'

export interface PlatformMeta {
  value: UpstreamPlatform
  label: string
  /** 选择卡片上的一句话说明。 */
  description: string
  /** 密钥前缀要求。空串表示后端不校验前缀。 */
  keyPrefix: string
  /** 密钥输入框的空态文案。 */
  keyPlaceholder: string
  /** 密钥格式提示，跟在输入框下面。 */
  keyHint: string
  /** 是否有 account_mode / base_url 这两个专属字段。 */
  usesMode: boolean
  tone: PlatformTone
}

/** 顺序即表单里卡片的顺序，Command Code 放在前面。 */
export const PLATFORMS: readonly PlatformMeta[] = [
  {
    value: 'commandcode',
    label: 'Command Code',
    description: '官方 Command Code 账号，密钥必须以 user_ 开头。',
    keyPrefix: 'user_',
    keyPlaceholder: 'user_xxxxxxxxxxxx',
    keyHint: '密钥必须以 user_ 开头',
    usesMode: false,
    tone: 'accent',
  },
  {
    value: 'opencode',
    label: 'OpenCode',
    description: 'OpenCode 账号，按 Zen / Go 两种计费模式走。',
    keyPrefix: '',
    keyPlaceholder: '粘贴 OpenCode 密钥',
    keyHint: 'OpenCode 密钥没有前缀要求，原样粘贴即可',
    usesMode: true,
    tone: 'info',
  },
]

export interface AccountModeMeta {
  value: AccountMode
  label: string
  description: string
  /** base_url 留空时后端会用的地址。 */
  defaultBaseUrl: string
}

export const ACCOUNT_MODES: readonly AccountModeMeta[] = [
  {
    value: 'zen',
    label: 'Zen 按量付费',
    description: '按实际 token 用量计费',
    defaultBaseUrl: 'https://opencode.ai/zen/v1',
  },
  {
    value: 'go',
    label: 'Go 订阅额度',
    description: '消耗 OpenCode Go 订阅额度',
    defaultBaseUrl: 'https://opencode.ai/zen/go/v1',
  },
]

/**
 * 取平台元数据。匹配不到时回落到 Command Code：
 * 老账号的 platform 可能是空串，那时它就是个普通的 user_ 账号。
 */
export function platformMeta(platform: string): PlatformMeta {
  return PLATFORMS.find((item) => item.value === platform) ?? PLATFORMS[0]!
}

/** 取计费模式元数据。未选模式时回落到 Zen，只为拿到默认地址做占位提示。 */
export function accountModeMeta(mode: string): AccountModeMeta {
  return ACCOUNT_MODES.find((item) => item.value === mode) ?? ACCOUNT_MODES[0]!
}

/** 按平台规则校验单把密钥，批量导入的逐行预检直接用这个。 */
export function isValidKey(platform: string, key: string): boolean {
  const prefix = platformMeta(platform).keyPrefix
  return prefix ? key.startsWith(prefix) : key.length > 0
}
