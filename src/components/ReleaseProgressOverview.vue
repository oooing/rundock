<script setup lang="ts">
import { computed } from 'vue'
import { tr } from '@/i18n'
import { artifactSizeSummary, progressLabels, releaseProgress } from '@/utils/releaseProgress'
import type { ProgressRun } from '@/utils/releaseProgress'
import type { CloudBuildStatus, ReleaseTarget } from '@/types'
import type { ReleaseDelivery, ReleaseTargetRun } from '@/types/releaseExecution'
const props = withDefaults(defineProps<{
  run: ProgressRun; targets?: ReleaseTargetRun[]; deliveries?: ReleaseDelivery[]; definitions?: ReleaseTarget[];
  artifacts?: Array<{ sizeBytes?: number }>; cloudHandoff?: boolean; cloudBuild?: CloudBuildStatus | null; localOnly?: boolean;
}>(), { targets: () => [], deliveries: () => [], definitions: () => [], artifacts: () => [] })
const progress = computed(() => releaseProgress({ ...props, deliveryPlanned: ['queued', 'running'].includes(props.run.status) && props.run.selectedTargets.some(choice => choice.publish && props.definitions.some(item => item.id === choice.targetId && item.delivery)) }))
const size = computed(() => artifactSizeSummary(props.artifacts))
const currentName = computed(() => {
  const current = progress.value.currentDelivery
  if (current) return props.run.versions?.find(version => version.versionGroupId === current.groupId)?.versionGroupName || current.groupId
  return props.definitions.find(item => item.id === progress.value.activeTarget?.targetId)?.name || progress.value.activeTarget?.targetId || ''
})
</script>

<template>
  <section class="release-progress-overview" :class="progress.state" :aria-label="tr('发布进度')">
    <div class="progress-topline">
    <div class="progress-headline" role="status" aria-live="polite" aria-atomic="true">
      <div class="status-symbol">
      <progress v-if="progress.state === 'running'" class="release-spinner" :aria-label="tr(progress.title)"></progress>
      <span v-else class="result-symbol" aria-hidden="true">{{ progress.state === 'succeeded' ? '✓' : progress.state === 'failed' ? '!' : '−' }}</span>
      </div>
      <div class="headline-copy"><span class="progress-eyebrow">{{ tr('发布进度') }} · {{ tr(progressLabels[progress.state]) }}</span><strong>{{ tr(progress.title, [tr(progress.phaseLabel)]) }}</strong><span v-if="currentName && progress.state !== 'succeeded'">{{ currentName }}</span></div>
    </div>
    <slot name="actions"></slot>
    </div>
    <p v-if="progress.error" class="progress-error" role="alert">{{ tr(progress.error) }}</p>
    <ol class="release-step-list" :style="{ '--step-count': progress.steps.length }" :aria-label="tr('本次流程')">
      <li v-for="(step, index) in progress.steps" :key="step.id" :class="step.state" :aria-current="step.state === 'running' ? 'step' : undefined">
        <span class="step-mark" aria-hidden="true">{{ step.state === 'succeeded' ? '✓' : step.state === 'failed' ? '!' : step.state === 'cancelled' || step.state === 'skipped' ? '−' : index + 1 }}</span>
        <span class="step-copy"><span class="step-label">{{ tr(step.label) }}</span><small>{{ tr(progressLabels[step.state]) }}</small></span>
      </li>
    </ol>
    <div class="progress-meta">
      <span v-if="progress.next">{{ tr('接下来：{0}', [tr(progress.next)]) }}</span>
      <span v-if="deliveries.length">{{ tr('Release 已发布 {0}/{1}', [progress.published, deliveries.length]) }}</span>
      <span v-if="artifacts.length" class="artifact-total">{{ tr('产物 {0} 个 · {1}', [artifacts.length, size.size]) }}<template v-if="size.missing"> · {{ tr('{0} 个大小未知', [size.missing]) }}</template></span>
    </div>
    <p v-if="progress.syncPending" class="sync-pending">{{ tr('GitHub 发布已完成；服务器同步尚未确认，请查看下方状态。') }}</p>
  </section>
</template>

<style scoped>
.release-progress-overview { --status-color: #93beff; --status-tint: #213047; flex-shrink: 0; padding: 20px; border-bottom: 1px solid var(--border, #39424e); background: #191e27; color: var(--text, #e5e9ef); min-width: 0; }
.release-progress-overview.succeeded { --status-color: #7ce6b9; --status-tint: #1c4035; background: #172720; border-bottom-color: #345749; }
.release-progress-overview.failed { --status-color: #ffaaaa; --status-tint: #542f37; background: #2c1e25; border-bottom-color: #73424c; }
.release-progress-overview.cancelled { --status-color: #f3cf8c; --status-tint: #443922; }
.progress-topline { display: flex; align-items: center; justify-content: space-between; gap: 20px; }
.progress-headline { display: flex; align-items: center; gap: 14px; color: var(--status-color); min-width: 0; }
.status-symbol { display: grid; place-items: center; width: 48px; height: 48px; flex-shrink: 0; border-radius: 14px; background: var(--status-tint); color: inherit; }
.headline-copy { display: grid; gap: 4px; min-width: 0; }.headline-copy strong { font-size: 20px; line-height: 1.35; overflow-wrap: anywhere; }.headline-copy > span { font-size: 12px; color: var(--text-dim, #acb5c4); }.headline-copy .progress-eyebrow { font-size: 11px; color: var(--status-color); }.result-symbol { font-size: 28px; font-weight: 700; }
.release-spinner { appearance: none; width: 22px; height: 22px; flex-shrink: 0; border: 3px solid #85b7ff30; border-top-color: currentColor; border-radius: 50%; background: transparent; animation: release-spin .9s linear infinite; }
.release-spinner::-webkit-progress-bar { display: none; }.release-spinner::-moz-progress-bar { background: transparent; }
@keyframes release-spin { to { transform: rotate(360deg); } }
.release-step-list { list-style: none; padding: 0; margin: 22px 0 16px; display: grid; grid-template-columns: repeat(var(--step-count), minmax(0, 1fr)); gap: 6px; }
.release-step-list li { position: relative; display: flex; flex-direction: column; align-items: center; gap: 9px; text-align: center; font-size: 11px; line-height: 1.5; color: #929cab; min-width: 0; }
.release-step-list li:not(:last-child)::after { content: ''; position: absolute; height: 1px; background: #35404d; top: 13px; left: calc(50% + 20px); width: calc(100% - 34px); }
.step-mark { display: grid; place-items: center; width: 28px; height: 28px; box-sizing: border-box; border: 1px solid #47515f; border-radius: 50%; font-size: 12px; font-weight: 600; }
.step-copy { padding: 0 3px; overflow-wrap: anywhere; }.step-label { display: block; min-height: 3em; text-wrap: balance; }.release-step-list small { display: block; font-size: 10px; margin-top: 3px; font-weight: 400; }
.release-step-list .succeeded { color: #7fd8b3; }li.succeeded .step-mark { border-color: #3c7a64; background: #203c32; }
.release-step-list .running { color: #acd0ff; font-weight: 650; }li.running .step-mark { color: #111e33; border-color: #93beff; background: #93beff; box-shadow: 0 0 0 4px #93beff16; }
.release-step-list .failed { color: #ffaaaa; font-weight: 650; }li.failed .step-mark { color: #2b161c; border-color: #ffaaaa; background: #ffaaaa; }
.release-step-list .cancelled { color: #f3cf8c; }.release-step-list .skipped { color: #929cab; }
.progress-meta { display: flex; flex-wrap: wrap; gap: 6px 16px; padding-top: 12px; border-top: 1px solid #ffffff0d; font-size: 11px; color: var(--text-dim, #acb5c4); }.progress-meta:empty { display: none; }.artifact-total { margin-left: auto; }
.progress-error { margin: 16px 0 0; padding: 12px 14px; border: 1px solid #94535f; border-radius: 8px; background: #482932; font-size: 13px; line-height: 1.6; color: #ffe0e0; overflow-wrap: anywhere; max-height: 7em; overflow: auto; }.sync-pending { margin: 12px 0 0; color: #f3cf8c; font-size: 12px; }
@media (prefers-reduced-motion: reduce) { .release-spinner { animation: none; } }
@media (max-width: 600px) { .release-progress-overview { padding: 14px; }.headline-copy strong { font-size: 17px; }.progress-topline { flex-wrap: wrap; gap: 12px; }.progress-topline :deep(.release-cancel) { flex: 1 0 100%; }.release-step-list { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px 8px; margin: 16px 0 12px; }.release-step-list li { flex-direction: row; text-align: left; align-items: flex-start; gap: 6px; font-size: 10px; }.release-step-list li::after { display: none; }.step-mark { flex-shrink: 0; width: 20px; height: 20px; font-size: 10px; }.step-copy { padding: 0; }.release-step-list small { display: none; }.artifact-total { margin-left: 0; }.status-symbol { width: 40px; height: 40px; } }
@media (max-width: 600px) { .release-step-list li { font-size: 11px; }.step-label { min-height: 0; text-wrap: wrap; }.release-step-list small { display: block; position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); } }
</style>
