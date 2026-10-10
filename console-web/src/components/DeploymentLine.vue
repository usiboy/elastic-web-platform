<script setup lang="ts">
import type { Deployment } from '../lib/api'
import StatusPill from './StatusPill.vue'

defineProps<{
  deployment: Deployment
  currentProduction: boolean
  rollbackTarget?: boolean
  canPromote: boolean
  busy: boolean
}>()
defineEmits<{ (e: 'preview', deployment: Deployment): void; (e: 'promote', deployment: Deployment): void }>()

function dateLabel(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '时间待核验' : date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
</script>

<template>
  <article class="deployment-line">
    <div class="deployment-main">
      <div class="deployment-topline">
        <span class="mono deployment-id">{{ deployment.deploymentId }}</span>
        <StatusPill :state="deployment.observedState === 'preview_ready' ? 'preview_ready' : deployment.desiredState" />
        <span v-if="currentProduction" class="live-label">当前生产</span>
      </div>
      <p class="deployment-meta">
        <span>{{ deployment.sourceKind === 'github' ? 'Git 提交' : deployment.sourceKind === 'upload' ? 'Agent 上传' : '受控制品' }}</span>
        <span class="meta-separator">·</span>
        <span>{{ dateLabel(deployment.createdAt) }}</span>
        <span class="meta-separator">·</span>
        <span class="mono short-id">{{ deployment.sourceId || '来源待核验' }}</span>
      </p>
    </div>
    <div class="deployment-actions">
      <button v-if="deployment.observedState === 'preview_ready'" class="text-button" type="button" :disabled="busy" @click="$emit('preview', deployment)">访问预览 ↗</button>
      <span v-else class="muted-action">入口待读回</span>
      <button v-if="canPromote && deployment.observedState === 'preview_ready' && !currentProduction" class="small-button" type="button" :disabled="busy" @click="$emit('promote', deployment)">{{ rollbackTarget ? '回滚至此版本' : '设为生产' }}</button>
    </div>
  </article>
</template>
