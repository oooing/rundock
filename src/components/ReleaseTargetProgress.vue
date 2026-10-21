<script setup lang="ts">
import { computed } from 'vue'
import { tr } from '@/i18n'
import type { CloudBuildStatus, ReleaseTarget } from '@/types'
import type { ReleaseDelivery, ReleaseTargetRun } from '@/types/releaseExecution'
import { targetBuildProgress, targetUploadProgress } from '@/utils/releaseProgress'
const props = defineProps<{ targets: ReleaseTargetRun[]; deliveries: ReleaseDelivery[]; definitions: ReleaseTarget[]; cloudBuild?: CloudBuildStatus | null }>()
const rows = computed(() => props.targets.map(target => ({
  target, build: targetBuildProgress(target), delivery: targetUploadProgress(target, props.deliveries, props.definitions, props.cloudBuild),
  name: target.name || props.definitions.find(item => item.id === target.targetId)?.name || target.targetId,
})))
</script>

<template>
  <div class="target-progress-list" :aria-label="tr('平台进度')">
    <article v-for="row in rows" :key="row.target.targetId" class="target-progress-row">
      <strong>{{ row.name }}</strong>
      <div class="target-phase" :class="row.build.state"><small>{{ tr('生成结果') }}</small><span><span aria-hidden="true">{{ row.build.state === 'succeeded' ? '✓' : row.build.state === 'failed' ? '!' : '·' }}</span> {{ tr(row.build.label) }}</span></div>
      <div v-if="row.target.publish || row.target.deploy" class="target-phase" :class="row.delivery.state"><small>{{ tr(row.target.publish ? '上传状态' : '部署') }}</small><span><span aria-hidden="true">{{ row.delivery.state === 'succeeded' ? '✓' : row.delivery.state === 'failed' ? '!' : '·' }}</span> {{ tr(row.delivery.label) }}</span></div>
      <p v-if="row.delivery.error" class="target-error">{{ row.delivery.error }}</p>
    </article>
  </div>
</template>

<style scoped>
.target-progress-list { display: grid; margin: 10px 0 20px; border: 1px solid var(--border); border-radius: 10px; overflow: hidden; }
.target-progress-row { display: grid; grid-template-columns: minmax(0,1.2fr) minmax(0,1fr) minmax(0,1.3fr); gap: 14px; padding: 14px; align-items: center; font-size: 13px; }
.target-progress-row + .target-progress-row { border-top: 1px solid var(--border); }
.target-progress-row strong { overflow-wrap: anywhere; }.target-phase { display: grid; gap: 5px; color: var(--text-dim); overflow-wrap: anywhere; }.target-phase small { font-size: 11px; color: var(--text-dim); }
.target-phase.succeeded { color: #7ce6b9; }.target-phase.failed { color: #ffaaaa; }.target-phase.running { color: #93beff; }.target-error { grid-column: 1 / -1; margin: 0; color: #ffaaaa; overflow-wrap: anywhere; }
@media (max-width: 600px) { .target-progress-row { grid-template-columns: minmax(0,1fr) minmax(0,1fr); gap: 10px; }.target-progress-row > strong { grid-column: 1 / -1; } }
</style>
