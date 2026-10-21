<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { tr } from '@/i18n'
import { getAppVersion, openProjectReleases } from '@/tauri/window'
import { appUpdate as update, dismissAppUpdate, downloadAppUpdate, stageAppUpdate, installAppUpdate, planAppUpdate, cancelAppUpdatePlan, cancelScheduledUpdate, retryUpdateRestore, recheckAppUpdate, checkAppUpdate } from '@/stores/appUpdate'

const currentVersion = ref('')
const dialog = ref<HTMLElement | null>(null)
const percent = computed(() => update.total ? Math.min(100, Math.floor(update.downloaded / update.total * 100)) : 0)
const installing = computed(() => update.phase === 'installing')
const busy = computed(() => installing.value || update.phase === 'planning')
const backendPending = computed(() => !!update.report?.backendVersion && update.report.backendVersion !== (update.report.currentVersion || currentVersion.value))
const title = computed(() => update.phase === 'installed' ? tr('新版已安装') : update.phase === 'staging' ? tr('正在静默安装') : update.phase === 'confirming' ? tr('确认立即生效') : update.phase === 'scheduled' ? tr('已安排安装') : update.phase === 'restoring' ? tr('正在恢复项目') : update.phase === 'installing' ? tr('正在更新') : update.phase === 'planning' ? tr('正在读取项目') : update.phase === 'downloading' ? tr('正在下载') : update.phase === 'finished' ? tr(update.report?.statusError ? '状态待确认' : update.report?.versionActive ? (update.report.remaining.length ? '项目待恢复' : '更新完成') : update.error ? '更新未完成' : update.report?.remaining.length ? '部分项目待恢复' : '更新完成') : update.phase === 'current' ? tr('当前已是新版') : update.info ? tr('新版可用') : tr('应用更新'))
const actualVersion = computed(() => update.report?.currentVersion || currentVersion.value)
const finished = computed(() => update.phase === 'finished')
let previousFocus: HTMLElement | null = null
const linkError = ref('')
async function openReleases() {
  try { await openProjectReleases() }
  catch { linkError.value = tr('未能打开 GitHub，请复制链接到浏览器查看。') }
}
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); dismissAppUpdate() }
  if (event.key !== 'Tab') return
  const elements = [...dialog.value!.querySelectorAll<HTMLElement>('button:not(:disabled), a, summary')].filter(el => el.getClientRects().length)
  const first = elements[0], last = elements[elements.length - 1]
  if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.value)) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && (document.activeElement === last || !dialog.value?.contains(document.activeElement) || document.activeElement === dialog.value)) { event.preventDefault(); first?.focus() }
}
onMounted(() => {
  previousFocus = document.activeElement as HTMLElement | null
  dialog.value?.focus()
  document.addEventListener('keydown', onKeydown)
  void getAppVersion().then(version => { currentVersion.value = version })
})
onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <div class="update-overlay" @click.self="dismissAppUpdate">
    <section ref="dialog" class="update-dialog" role="dialog" aria-modal="true" aria-labelledby="update-title" tabindex="-1">
      <header>
        <h2 id="update-title">{{ finished && backendPending && !update.report?.statusError && !update.report?.remaining.length ? tr('后台待更新') : title }}</h2>
        <button class="ghost icon" :aria-label="tr('关闭')" :disabled="busy" @click="dismissAppUpdate">✕</button>
      </header>
      <p v-if="finished" class="version">
        {{ tr('当前版本') }} <strong>v{{ actualVersion }}</strong>
        <span v-if="update.report?.backendVersion && update.report.backendVersion !== actualVersion"> · {{ tr('后台版本') }} v{{ update.report.backendVersion }}</span>
        <span v-if="!update.report?.versionActive && update.info"> · {{ tr('本次目标') }} v{{ update.info.version }}</span>
      </p>
      <p v-else-if="update.info" class="version"><span v-if="currentVersion && currentVersion !== update.info.version">v{{ currentVersion }} <span aria-hidden="true">→</span> </span><strong>v{{ update.info.version }}</strong></p>
      <div class="update-body">
        <div v-if="update.phase === 'downloading'" class="download-status" role="status">
          <div><span>{{ tr('正在后台下载更新') }}</span><span>{{ percent }}%</span></div>
          <progress :value="update.downloaded" :max="update.total || 1" :aria-label="tr('下载进度')"></progress>
        </div>
        <p v-else-if="update.phase === 'ready'" role="status">{{ tr('静默安装后，下次启动生效。项目保持运行。') }}</p>
        <p v-else-if="update.phase === 'staging'" role="status">{{ tr('正在后台静默安装，可关闭此窗口。项目保持运行。') }}</p>
        <template v-else-if="update.phase === 'installed'">
          <p role="status">{{ tr('新版已安装，下次启动生效。项目保持运行。') }}</p>
          <p class="hint">{{ tr('需完整退出后再打开；收起到托盘不算退出。') }}</p>
        </template>
        <p v-else-if="installing" role="status">{{ tr('正在启用新版，完成后尝试重新启动项目。') }}</p>
        <template v-else-if="update.phase === 'confirming'">
          <p>{{ tr('将停止以下项目，超时会结束其进程。请先保存工作，调试会话不会保留。') }}</p>
          <ul class="project-list"><li v-for="project in update.plan?.projects" :key="project.id">{{ project.name }}</li></ul>
          <p v-if="!update.plan?.projects.length">{{ tr('没有运行中的项目。') }}</p>
          <p class="hint">{{ tr('完成后尝试重新启动；失败会列出原因。') }}</p>
        </template>
        <p v-else-if="update.phase === 'scheduled'" role="status">{{ tr('已安排下次启动安装，也可现在静默安装。项目保持运行。') }}</p>
        <template v-else-if="update.phase === 'finished'">
          <p v-if="update.report?.statusError" class="error" role="alert">{{ update.report.statusError }}</p>
          <p v-else-if="backendPending" class="hint">{{ tr('窗口已更新，后台仍是旧版。继续更新可启用新版后台。') }}</p>
          <p v-else-if="update.report?.versionActive && update.report.remaining.length" class="ready">{{ tr('版本已生效，以下项目尚未恢复。') }}</p>
          <p v-if="!update.report?.statusError && !update.error && !update.report?.remaining.length" class="ready">{{ tr('项目已恢复，可以继续使用。') }}</p>
          <ul v-if="!update.report?.statusError && update.report?.failures.length" class="project-list error"><li v-for="failure in update.report.failures" :key="failure">{{ failure }}</li></ul>
          <p v-if="update.report?.remaining.length" class="hint">{{ tr('保持现状会结束本次恢复，不启动或停止项目。') }}</p>
        </template>
        <details v-if="update.info?.notes"><summary>{{ tr('更新说明') }}</summary><pre>{{ update.info.notes }}</pre></details>
        <div v-if="update.error && !update.report?.statusError" class="error" role="alert">
          {{ update.error }}
          <button class="link" @click="openReleases">{{ tr('查看版本下载页') }} ↗</button>
          <p v-if="linkError">{{ linkError }} https://github.com/oooing/rundock/releases</p>
        </div>
      </div>
      <footer>
        <template v-if="update.phase === 'installed'">
          <button @click="planAppUpdate">{{ tr('立即生效') }}</button>
          <button class="primary" @click="dismissAppUpdate">{{ tr('知道了') }}</button>
        </template>
        <template v-else-if="update.phase === 'ready'">
          <button class="primary" @click="stageAppUpdate">{{ update.error ? tr('重试静默安装') : tr('静默安装') }}</button>
        </template>
        <template v-else-if="update.phase === 'confirming'">
          <button @click="cancelAppUpdatePlan">{{ tr('返回') }}</button>
          <button class="danger" @click="installAppUpdate">{{ tr('停止项目并重启') }}</button>
        </template>
        <template v-else-if="update.phase === 'scheduled'">
          <button class="primary" @click="stageAppUpdate">{{ update.error ? tr('重试静默安装') : tr('静默安装') }}</button>
        </template>
        <template v-else-if="update.phase === 'staging'">
          <button class="primary" @click="dismissAppUpdate">{{ tr('知道了') }}</button>
        </template>
        <template v-else-if="update.phase === 'finished'">
          <button @click="cancelScheduledUpdate(!!update.report?.remaining.length)">{{ tr(update.report?.remaining.length ? '保持现状' : '关闭') }}</button>
          <button v-if="update.report?.remaining.length" class="primary" :disabled="!!update.report.statusError" @click="retryUpdateRestore">{{ tr('恢复项目') }}</button>
          <button v-else-if="backendPending && !update.report?.statusError" class="primary" @click="recheckAppUpdate">{{ tr('继续更新') }}</button>
          <button v-else-if="update.error" class="primary" @click="recheckAppUpdate">{{ tr('重新检查') }}</button>
        </template>
        <template v-else>
          <button :disabled="busy" @click="dismissAppUpdate">{{ tr('稍后') }}</button>
          <button v-if="update.phase === 'available'" class="primary" @click="downloadAppUpdate">{{ update.error ? tr('重试下载') : tr('下载更新') }}</button>
          <button v-else-if="['idle','current'].includes(update.phase)" class="primary" @click="checkAppUpdate()">{{ tr('重新检查') }}</button>
          <button v-else class="primary" disabled>{{ installing ? tr('正在安装…') : update.phase === 'planning' ? tr('正在读取…') : update.phase === 'restoring' ? tr('正在恢复…') : update.phase === 'checking' ? tr('正在检查…') : tr('正在下载…') }}</button>
        </template>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.update-overlay { position: fixed; inset: 0; z-index: 220; display: grid; place-items: center; padding: 20px; background: #0009; }
.update-dialog { width: min(100%, 420px); max-height: 88vh; overflow: auto; padding: 24px; border: 1px solid var(--border); border-radius: 14px; background: var(--bg-elev); box-shadow: 0 20px 80px #0006; outline: none; }
header { display: flex; align-items: center; justify-content: space-between; }
h2 { margin: 0; font-size: 20px; }
.version { margin: 6px 0 16px; font-size: 12px; color: var(--text-faint); }
.version strong { color: var(--text-dim); font-weight: 500; }
.version span[aria-hidden] { padding: 0 5px; }
.update-body { font-size: 13px; line-height: 1.6; }
details { margin-top: 12px; font-size: 12px; }
summary { cursor: pointer; color: var(--text-dim); }
pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 180px; overflow: auto; font: inherit; margin: 12px 0 0; }
.download-status { margin-top: 18px; }
.download-status div { display: flex; justify-content: space-between; color: var(--text-dim); }
progress { width: 100%; height: 6px; accent-color: var(--accent); }
.ready { color: var(--green); }
.hint { color: var(--text-faint); font-size: 12px; }
.error { color: var(--red); margin-top: 12px; overflow-wrap: anywhere; }
.link { display: block; border: 0; background: transparent; color: var(--accent-hover); padding: 6px 0 0; }
.project-list { max-height: 180px; overflow: auto; padding-left: 20px; }
.danger { background: var(--red); color: white; }
footer { display: flex; justify-content: flex-end; gap: 10px; margin-top: 24px; }
</style>
