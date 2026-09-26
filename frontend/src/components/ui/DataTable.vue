<script setup lang="ts">
import { computed } from 'vue'

import Skeleton from './Skeleton.vue'

// 表格外壳：统一边框、横向滚动和加载/空态。
//
// 刻意不做「列配置」式的通用表格——列渲染差异太大，抽象出来只会
// 变成一堆条件分支。这里只负责容器与状态，列 markup 留在各视图里。
const props = withDefaults(
  defineProps<{
    columns: number
    loading?: boolean
    empty?: boolean
    skeletonRows?: number
    /** 行悬停高亮：行可点击时打开。 */
    hoverable?: boolean
    /** 单元格外加的最小宽度，避免中文列被压成两行。 */
    minWidth?: string
  }>(),
  { loading: false, empty: false, skeletonRows: 6, hoverable: false, minWidth: '100%' },
)

const tableStyle = computed(() => ({ minWidth: props.minWidth }))

</script>

<template>
  <div class="overflow-hidden rounded-panel border border-line bg-surface">
    <div class="scroll-thin overflow-x-auto">
      <table class="w-full border-collapse text-left" :style="tableStyle">
        <thead class="border-b border-line bg-raised/60">
          <tr>
            <slot name="head" />
          </tr>
        </thead>

        <tbody v-if="loading" class="divide-y divide-line">
          <tr v-for="row in skeletonRows" :key="row">
            <td v-for="col in columns" :key="col" class="px-3 py-2.5">
              <Skeleton class="h-3.5" :class="col === 1 ? 'w-32' : 'w-16'" />
            </td>
          </tr>
        </tbody>

        <tbody v-else-if="empty">
          <tr>
            <td :colspan="columns" class="px-4 py-12">
              <slot name="empty" />
            </td>
          </tr>
        </tbody>

        <tbody
          v-else
          class="divide-y divide-line"
          :class="hoverable ? '[&>tr:hover]:bg-raised/50' : ''"
        >
          <slot name="body" />
        </tbody>
      </table>
    </div>

    <slot name="footer" />
  </div>
</template>
