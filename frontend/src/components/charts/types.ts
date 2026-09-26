// 图表组件的输入类型。图表只认「一串带标签的数值」，
// 具体业务语义由调用方通过 formatter 注入。
export interface ChartPoint {
  /** 轴上的短标签，例如 "09-26 14:00"。 */
  label: string
  value: number
  /** 悬停时显示的完整描述，缺省时回退到 label。 */
  tooltip?: string
}

export interface BarItem {
  key: string
  label: string
  value: number
  /** 次要信息（token 数、费用），显示在数值旁边。 */
  secondary?: string
}
