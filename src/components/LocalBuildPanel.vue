<script setup lang="ts">
import { computed } from 'vue'
import { tr } from '@/i18n'
import { useLocalBuilds } from '@/composables/useLocalBuilds'
import type { LocalBuildRun, LocalBuildTarget } from '@/types/localBuild'
import CopyErrorButton from './CopyErrorButton.vue'
import LocalBuildArtifacts from './LocalBuildArtifacts.vue'

const props = defineProps<{ appId: string; appName: string }>()
const emit = defineEmits<{ (event: 'settings'): void }>()
const { preparation, recentRuns, selectedIds, view, pendingRequest, loading, submitting,
  cancelling, readingRun, error, runError, actionError, cancelError, active, existingActive, busy, canStart,
  load, start, cancel, showRun, readTaskStatus, newBuild } = useLocalBuilds(() => props.appId)
const hasAvailableTargets = computed(() => preparation.value?.targets.some(target => target.available))
const statusLabel = (status: LocalBuildRun['status']) => tr({ queued: '等待构建', running: '构建中',
  succeeded: '本地构建完成', failed: '本地构建失败', cancelled: '已取消构建' }[status])
const stageLabel = (stage: string) => tr(({ prepare: '准备构建', check: '检查', build: '构建',
  package: '打包', verify: '验证产物', local_freezing: '准备构建', local_check: '检查',
  local_build: '构建', local_package: '打包', local_verifying: '验证产物', local_saving: '保存产物',
  completed: '完成', complete: '完成', done: '完成', cancelled: '已取消构建' } as Record<string, string>)[stage] || '等待执行')
const phaseLabels = (target: LocalBuildTarget) => [target.check && tr('检查'), target.build && tr('构建'),
  target.package && tr('打包')].filter(Boolean).join(' → ')
const targetName = (id: string) => preparation.value?.targets.find(target => target.id === id)?.name || id
const failureText = computed(() => view.value ? [
  `RunDock · ${tr('本地构建')} · ${props.appName}`,
  `Run: ${view.value.run.id}`,
  `Project: ${view.value.run.repoRoot}`,
  `Created: ${view.value.run.createdAt}`,
  `${tr('状态')}: ${statusLabel(view.value.run.status)} · ${stageLabel(view.value.run.stage)}`,
  view.value.run.errorCode, view.value.run.errorMessage,
  ...view.value.targets.map(target => `${targetName(target.targetId)}: ${target.stage} · ${target.status} · ${target.errorCode} ${target.errorMessage}`),
  ...view.value.logs.slice(-100).map(line => `${line.ts} [${line.stream}] ${line.text}`),
].filter(Boolean).join('\n') : '')
function dateOf(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>

<template>
  <section id="release-panel-local-build" class="local-build-panel" role="tabpanel" aria-labelledby="release-tab-local-build" tabindex="0">
    <div class="local-build-intro">
      <h3>{{ tr('本地构建') }}</h3>
      <p>{{ tr('在本机生成安装包，不提交代码、不创建版本、不上传。') }}</p>
      <details class="local-build-help"><summary>{{ tr('操作说明') }}</summary><p>{{ tr('选目标 → 开始构建 → 打开产物目录/下载') }}</p><p>{{ tr('使用项目配置的命令；所需依赖仍需安装。源代码在隔离快照中构建。') }}</p></details>
    </div>

    <p v-if="loading && !view" class="muted" role="status">{{ tr('正在读取本地构建配置…') }}</p>
    <div v-if="error" class="local-error" role="alert"><p>{{ error }}</p><button v-if="!pendingRequest" type="button" :disabled="loading || busy" @click="load(true)">{{ tr('重新读取配置') }}</button><button v-if="!preparation" type="button" @click="emit('settings')">{{ tr('配置构建目标') }}</button></div>
    <p v-if="actionError" class="local-error" role="alert">{{ actionError }}</p>
    <div v-if="pendingRequest" class="pending-request" role="status">
      <p>{{ submitting ? tr('正在创建构建任务…') : tr('上次请求结果待确认；确认后不会重复创建任务。') }}</p>
      <button v-if="!submitting" type="button" :disabled="busy" @click="start(true)">{{ tr('确认任务状态') }}</button>
    </div>

    <template v-if="!view && preparation && !pendingRequest">
      <form :action="`/api/apps/${encodeURIComponent(appId)}/local-builds`" method="post" @submit.prevent="start()">
        <fieldset v-if="preparation.targets.length" class="local-build-targets" :disabled="busy || loading" :aria-busy="loading">
          <legend>{{ tr('选择构建目标') }}</legend>
          <div v-for="target in preparation.targets" :key="target.id" class="local-build-target" :class="{ unavailable: !target.available, selected: selectedIds.includes(target.id) }">
            <input :id="`local-build-target-${target.id}`" v-model="selectedIds" name="targetIds" type="checkbox" :value="target.id" :disabled="busy || loading || !target.available" :aria-describedby="`local-build-detail-${target.id}`" />
            <label :for="`local-build-target-${target.id}`"><strong>{{ target.name }}</strong><span>{{ tr('当前版本') }} {{ target.currentVersion || '—' }}</span></label>
            <p :id="`local-build-detail-${target.id}`">{{ target.available ? phaseLabels(target) : tr(target.reason || '此目标暂不可用') }}</p>
          </div>
        </fieldset>
        <div v-if="!hasAvailableTargets" class="local-empty">
          <p>{{ tr('请在设置中配置本地构建或打包命令，以及产物位置。') }}</p>
          <button type="button" @click="emit('settings')">{{ tr('配置构建目标') }}</button>
        </div>
        <div v-else class="local-build-actions">
          <p v-if="existingActive" class="muted">{{ tr('已有本地构建任务正在运行，请打开任务查看。') }}<button type="button" @click="showRun(existingActive)">{{ tr('查看任务') }}</button></p>
          <p v-else-if="!selectedIds.length" class="muted">{{ tr('请选择至少一个构建目标。') }}</p>
          <button type="submit" class="primary local-build-start" :disabled="!canStart" :aria-busy="submitting">{{ tr('开始构建') }}</button>
        </div>
      </form>
    </template>

    <section v-if="view" class="local-build-run">
      <div class="run-heading"><h4 class="local-build-status" :class="view.run.status" role="status" aria-live="polite">{{ statusLabel(view.run.status) }}</h4><button v-if="active" type="button" :disabled="busy" :aria-busy="cancelling" @click="cancel()">{{ cancelling ? tr('正在取消…') : tr('取消构建') }}</button><button v-else type="button" :disabled="busy || readingRun" @click="newBuild">{{ tr('新建构建') }}</button></div>
      <p v-if="active" class="muted">{{ stageLabel(view.run.stage) }} · {{ tr('关闭此窗口不会停止构建；可从最近任务取回结果。') }}</p>
      <p v-else-if="view.run.status === 'succeeded'" class="muted">{{ tr('产物已验证并保存在本机，可打开目录或下载。') }}</p>
      <p v-else-if="view.run.status === 'cancelled'" class="muted">{{ tr('任务已停止；已保存的产物仍可取回。') }}</p>
      <p v-if="view.run.errorMessage" class="local-error">{{ tr(view.run.errorMessage) }}</p>
      <CopyErrorButton v-if="view.run.status === 'failed'" :text="failureText" />
      <div v-if="runError" class="local-error" role="alert"><p>{{ runError }}</p><button type="button" :disabled="readingRun || busy" @click="readTaskStatus()">{{ tr('重新读取任务') }}</button></div>
      <div v-if="cancelError" class="local-error" role="alert"><p>{{ cancelError }}</p><button v-if="active" type="button" :disabled="busy" @click="cancel()">{{ tr('重试取消') }}</button><button type="button" :disabled="readingRun || busy" @click="readTaskStatus()">{{ tr('重新读取任务') }}</button></div>
      <p v-else-if="readingRun && !view.targets.length" class="muted">{{ tr('正在读取任务…') }}</p>
      <ul v-if="view.targets.length" class="run-target-list">
        <li v-for="target in view.targets" :key="target.targetId"><strong>{{ targetName(target.targetId) }}</strong><span v-if="!['completed', 'complete', 'done', 'cancelled'].includes(target.stage)">{{ stageLabel(target.stage) }}</span><span>{{ target.status === 'cancelled' ? tr('已取消构建') : target.status === 'failed' ? tr('失败') : target.status === 'succeeded' ? tr('完成') : tr('执行中') }}</span></li>
      </ul>
      <LocalBuildArtifacts v-if="!active || view.artifacts.length" :run-id="view.run.id" :artifacts="view.artifacts" :output-directory="view.outputDirectory" />
      <details class="local-build-logs"><summary>{{ tr('执行日志') }}</summary><pre v-if="view.logs.length"><span v-for="line in view.logs" :key="line.id" :class="line.stream">{{ line.text }}{{ '\n' }}</span></pre><p v-else class="muted">{{ tr('暂无构建日志。') }}</p></details>
    </section>

    <details v-if="recentRuns.length" class="local-build-history" :open="!view">
      <summary>{{ tr('最近构建任务') }}</summary>
      <ul><li v-for="run in recentRuns" :key="run.id"><button type="button" :disabled="busy || view?.run.id === run.id" :aria-label="tr('打开构建任务 {0}', [dateOf(run.createdAt)])" @click="showRun(run)"><span>{{ dateOf(run.createdAt) }}</span><span>{{ statusLabel(run.status) }}</span></button></li></ul>
    </details>
  </section>
</template>

<style scoped>
.local-build-panel { display: grid; gap: 1rem; outline-offset: -2px; }
.local-build-intro h3 { margin: 0; font-size: 1.1rem; }
.local-build-intro > p { margin-block: .5rem; line-height: 1.6; }
.local-build-help, .muted, .local-build-target p { color: var(--text-muted, #b4bdc9); font-size: .875rem; }
.local-build-help p { margin-block: .6rem; max-width: 80ch; }
summary { cursor: pointer; padding-block: .4rem; }
fieldset { border: 0; padding: 0; margin: 0; min-width: 0; }
legend { margin-block-end: .6rem; font-weight: 600; }
.local-build-target { display: grid; grid-template-columns: 1.1rem minmax(0, 1fr); column-gap: .8rem; padding: .85rem; margin-block-end: .5rem; border: 1px solid var(--border, #39424e); border-radius: .6rem; }
.local-build-target.selected { border-color: #73a9ff; background: #518bff10; }
.local-build-target.unavailable { background: #ffffff04; }
.local-build-target input { margin: .15rem 0 0; width: 1.1rem; height: 1.1rem; accent-color: #76a8ff; }
.local-build-target label { display: flex; flex-wrap: wrap; gap: .5rem 1rem; cursor: pointer; }
.local-build-target label span { color: var(--text-muted, #b4bdc9); font-size: .85rem; }
.local-build-target p { grid-column: 2; margin: .4rem 0 0; overflow-wrap: anywhere; }
.local-build-actions { display: flex; align-items: center; justify-content: flex-end; flex-wrap: wrap; gap: .75rem; margin-block-start: .8rem; }
.local-build-actions p { margin: 0; margin-inline-end: auto; }
.run-heading { display: flex; justify-content: space-between; align-items: center; gap: .75rem; flex-wrap: wrap; }
.run-heading h4 { margin: 0; font-size: 1rem; }
.succeeded { color: #70e4b2; }.failed, .local-error { color: #ffa4a4; }
.local-error, .pending-request, .local-empty { padding: .8rem; border: 1px solid var(--border, #39424e); border-radius: .6rem; overflow-wrap: anywhere; }
.local-error p, .pending-request p, .local-empty p { margin-block: 0 .65rem; }
.run-target-list, .local-build-history ul { list-style: none; margin: .75rem 0; padding: 0; }
.run-target-list li { display: flex; justify-content: space-between; gap: .5rem; flex-wrap: wrap; padding-block: .5rem; font-size: .85rem; }
.local-build-logs, .local-build-history { border-block-start: 1px solid var(--border, #39424e); padding-block-start: .65rem; margin-block-start: .85rem; }
.local-build-logs pre { max-height: 16rem; overflow: auto; padding: .75rem; background: #0003; font-size: .8rem; white-space: pre-wrap; overflow-wrap: anywhere; }
.local-build-logs .stderr, .local-build-logs .error { color: #ffa4a4; }
.local-build-history button { width: 100%; display: flex; justify-content: space-between; gap: .65rem; flex-wrap: wrap; margin-block-end: .4rem; text-align: start; }
button { min-height: 2.5rem; }button:focus-visible, input:focus-visible, summary:focus-visible { outline: 2px solid #8dbbff; outline-offset: 3px; }
@media (pointer: coarse) { button { min-height: 3rem; } }
</style>
