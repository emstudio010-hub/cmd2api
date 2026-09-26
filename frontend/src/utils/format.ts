// 展示层的格式化统一放这里。视图里不写 `toFixed(2)`——同一个数字
// 在概览卡和表格里显示成不同精度是最容易被发现的不专业细节。

const numberFormatter = new Intl.NumberFormat('zh-CN')

/** 整数千分位。 */
export function formatNumber(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '—'
  return numberFormatter.format(Math.round(value))
}

/** 大数字的紧凑写法：tokens 动辄百万，表格里不适合铺满一列。 */
export function formatCompact(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '—'
  const abs = Math.abs(value)
  if (abs < 1000) return String(Math.round(value))
  if (abs < 1_000_000) return `${(value / 1000).toFixed(abs < 10_000 ? 2 : 1)}K`
  if (abs < 1_000_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  return `${(value / 1_000_000_000).toFixed(2)}B`
}

/**
 * 费用。上游按倍率折算后的数值通常很小，直接 toFixed(2) 会显示成 0.00，
 * 让人觉得「统计坏了」，所以按量级选精度。
 */
export function formatCost(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '—'
  const abs = Math.abs(value)
  if (abs === 0) return '0'
  if (abs < 0.01) return value.toFixed(6)
  if (abs < 1) return value.toFixed(4)
  if (abs < 1000) return value.toFixed(2)
  return numberFormatter.format(Math.round(value))
}

/** 成功率：后端给的是 0~1 的小数。 */
export function formatPercent(ratio: number | null | undefined, digits = 1): string {
  if (ratio === null || ratio === undefined || Number.isNaN(ratio)) return '—'
  return `${(ratio * 100).toFixed(digits)}%`
}

/** 耗时。大于 1 秒换算成秒，否则毫秒整数。 */
export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || Number.isNaN(ms)) return '—'
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

function parse(value: string | null | undefined): Date | null {
  if (!value) return null
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

function pad(value: number): string {
  return value < 10 ? `0${value}` : String(value)
}

/** 完整时间：表格里的主时间列用它。 */
export function formatDateTime(value: string | null | undefined): string {
  const date = parse(value)
  if (!date) return '—'
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  )
}

/** 只到分钟：看板的时间轴标签用它。 */
export function formatDateTimeShort(value: string | null | undefined): string {
  const date = parse(value)
  if (!date) return '—'
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/** 只到天：过期时间这类不需要精确到秒的字段。 */
export function formatDate(value: string | null | undefined): string {
  const date = parse(value)
  if (!date) return '—'
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

/** 相对时间，超过 7 天就退回完整时间，避免出现「42 天前」这种没用的信息。 */
export function formatRelative(value: string | null | undefined): string {
  const date = parse(value)
  if (!date) return '—'
  const diff = Date.now() - date.getTime()
  if (diff < 0) return formatDateTime(value)
  const seconds = Math.floor(diff / 1000)
  if (seconds < 45) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${Math.max(minutes, 1)} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days} 天前`
  return formatDateTime(value)
}

/** 过期时间的语义化展示：已过期要显式说出来。 */
export function formatExpiry(value: string | null | undefined): string {
  const date = parse(value)
  if (!date) return '永不过期'
  if (date.getTime() < Date.now()) return `已过期（${formatDate(value)}）`
  return formatDateTime(value)
}

export function isExpired(value: string | null | undefined): boolean {
  const date = parse(value)
  return date !== null && date.getTime() < Date.now()
}

/** ISO → `datetime-local` 输入框需要的本地时间字符串。 */
export function toLocalInputValue(value: string | null | undefined): string {
  const date = parse(value)
  if (!date) return ''
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}` +
    `T${pad(date.getHours())}:${pad(date.getMinutes())}`
  )
}

/** `datetime-local` 的值 → 后端要的 RFC3339。空值返回空串。 */
export function fromLocalInputValue(value: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toISOString()
}

/** 把令牌/密钥打断到中间省略，列表里不铺开一长串。 */
export function maskSecret(value: string, head = 8, tail = 4): string {
  if (!value) return '—'
  if (value.length <= head + tail + 3) return value
  return `${value.slice(0, head)}…${value.slice(-tail)}`
}

/** 字节数可读化，设置页用不上但日志里会用到。 */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || Number.isNaN(bytes)) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}
