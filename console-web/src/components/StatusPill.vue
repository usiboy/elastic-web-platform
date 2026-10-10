<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ state: string }>()
const label = computed(() => ({
  active: '入口已读回',
  preview_ready: '预览就绪',
  requested: '等待入口确认',
  candidate: '等待预览',
  not_deployed: '尚未部署',
  verified: '仓库已核验',
  pending: '等待仓库核验',
  revoked: '授权已撤销',
  pending_commit_verification: '正在核验来源',
  awaiting_isolated_build: '等待隔离构建',
  preview_candidate: '等待预览读回',
  production_requested: '等待生产读回',
  superseded: '已被新提交取代',
  failed: '处理失败',
}[props.state] || props.state))
const tone = computed(() => {
  if (props.state === 'active' || props.state === 'preview_ready' || props.state === 'verified') return 'positive'
  if (props.state === 'requested' || props.state === 'candidate' || props.state === 'pending' || props.state === 'pending_commit_verification' || props.state === 'awaiting_isolated_build' || props.state === 'preview_candidate' || props.state === 'production_requested') return 'waiting'
  if (props.state === 'revoked' || props.state === 'failed') return 'danger'
  return 'quiet'
})
</script>

<template>
  <span class="status-pill" :class="`status-pill--${tone}`"><span class="status-dot" />{{ label }}</span>
</template>
