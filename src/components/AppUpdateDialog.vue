<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { tr } from '@/i18n'
import { getAppVersion, openProjectReleases } from '@/tauri/window'
import { appUpdate as update, dismissAppUpdate, downloadAppUpdate, installAppUpdate, scheduleAppUpdate, planAppUpdate, cancelScheduledUpdate, retryUpdateRestore, recheckAppUpdate } from '@/stores/appUpdate'
import UiIcon from './UiIcon.vue'

const currentVersion = ref('')
const dialog = ref<HTMLElement | null>(null)
const percent = computed(() => update.total ? Math.min(100, Math.floor(update.downloaded / update.total * 100)) : 0)
const installing = computed(() => update.phase === 'installing')
const busy = computed(() => installing.value || update.phase === 'planning')
const title = computed(() => update.phase === 'confirming' ? tr('确认安装') : update.phase === 'scheduled' ? tr('已安排安装') : update.phase === 'restoring' ? tr('正在恢复项目') : update.phase === 'finished' ? tr(update.error ? '更新未完成' : update.report?.remaining.length ? '部分项目待恢复' : '更新完成') : tr('新版已准备好'))
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
        <div class="update-symbol"><UiIcon name="refresh" :size="22" /></div>
        <button class="ghost icon" :aria-label="tr('关闭')" :disabled="busy" @click="dismissAppUpdate">✕</button>
      </header>
      <h2 id="update-title">{{ title }}</h2>
      <p class="version">RunDock v{{ update.info?.version }} <span v-if="currentVersion">{{ tr('当前 v{0}', [currentVersion]) }}</span></p>
      <div class="update-body">
        <details v-if="update.info?.notes"><summary>{{ tr('更新说明') }}</summary><pre>{{ update.info.notes }}</pre></details>
        <div v-if="update.phase === 'downloading'" class="download-status" role="status">
          <div><span>{{ tr('正在后台下载更新') }}</span><span>{{ percent }}%</span></div>
          <progress :value="update.downloaded" :max="update.total || 1" :aria-label="tr('下载进度')"></progress>
        </div>
        <p v-else-if="update.phase === 'ready'" class="ready" role="status">{{ tr('下载完成，安装包已校验') }}</p>
        <p v-else-if="installing" role="status">{{ tr('正在停止项目、安装更新，完成后自动恢复项目…') }}</p>
        <p v-else-if="update.phase === 'planning'" role="status">{{ tr('正在读取将停止的项目…') }}</p>
        <template v-else-if="update.phase === 'confirming'">
          <p>{{ tr('立即安装会停止以下项目，并重启 RunDock。请先保存工作。') }}</p>
          <ul class="project-list"><li v-for="project in update.plan?.projects" :key="project.id">{{ project.name }}</li></ul>
          <p v-if="!update.plan?.projects.length">{{ tr('当前没有由 RunDock 托管的运行项目。') }}</p>
          <p class="install-note">{{ tr('安装完成后自动恢复这些项目，其他项目保持停止。') }}</p>
        </template>
        <p v-else-if="update.phase === 'scheduled'" class="ready" role="status">{{ tr('下次完整启动 RunDock 时自动安装。当前项目继续运行。') }}</p>
        <p v-else-if="update.phase === 'restoring'" role="status">{{ tr('正在恢复原先运行的项目，请稍候…') }}</p>
        <template v-else-if="update.phase === 'finished'">
          <p v-if="!update.error && !update.report?.remaining.length" class="ready">{{ tr('窗口和后台已更新，原先运行的项目已恢复。') }}</p>
          <ul v-if="update.report?.failures.length" class="project-list error"><li v-for="failure in update.report.failures" :key="failure">{{ failure }}</li></ul>
          <p v-if="update.report?.remaining.length" class="install-note">{{ tr('请查看项目记录，处理原因后点击重试恢复。') }}</p>
        </template>
        <p v-if="['ready','installed','scheduled'].includes(update.phase)" class="install-note">{{ tr('默认下次启动安装；从托盘打开窗口不会触发安装。') }}</p>
        <div v-if="update.error" class="error" role="alert">
          {{ update.error }}
          <button class="link" @click="openReleases">{{ tr('查看版本下载页') }} ↗</button>
          <p v-if="linkError">{{ linkError }} https://github.com/oooing/rundock/releases</p>
        </div>
      </div>
      <footer>
        <template v-if="['ready','installed'].includes(update.phase)">
          <button @click="planAppUpdate">{{ tr('立即安装') }}</button>
          <button class="primary" @click="scheduleAppUpdate">{{ tr('下次启动安装') }}</button>
        </template>
        <template v-else-if="update.phase === 'confirming'">
          <button @click="scheduleAppUpdate">{{ tr('下次启动安装') }}</button>
          <button class="danger" @click="installAppUpdate">{{ tr('停止项目并安装') }}</button>
        </template>
        <template v-else-if="update.phase === 'scheduled'">
          <button class="link" @click="cancelScheduledUpdate">{{ tr('取消安排') }}</button>
          <button @click="planAppUpdate">{{ tr('立即安装') }}</button>
          <button class="primary" @click="dismissAppUpdate">{{ tr('知道了') }}</button>
        </template>
        <template v-else-if="update.phase === 'finished'">
          <button @click="update.report?.remaining.length ? dismissAppUpdate() : cancelScheduledUpdate()">{{ tr('关闭') }}</button>
          <button v-if="update.report?.remaining.length" class="primary" @click="retryUpdateRestore">{{ tr('重试恢复') }}</button>
          <button v-else-if="update.error" class="primary" @click="recheckAppUpdate">{{ tr('重新检查') }}</button>
        </template>
        <template v-else>
          <button :disabled="busy" @click="dismissAppUpdate">{{ tr('稍后') }}</button>
          <button v-if="update.phase === 'available'" class="primary" @click="downloadAppUpdate">{{ update.error ? tr('重试下载') : tr('下载更新') }}</button>
          <button v-else class="primary" disabled>{{ installing ? tr('正在安装…') : update.phase === 'planning' ? tr('正在读取…') : update.phase === 'restoring' ? tr('正在恢复…') : tr('正在下载…') }}</button>
        </template>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.update-overlay { position: fixed; inset: 0; z-index: 220; display: grid; place-items: center; padding: 20px; background: #0009; }
.update-dialog { width: min(100%, 460px); max-height: 88vh; overflow: auto; padding: 24px; border: 1px solid var(--border); border-radius: 16px; background: var(--bg-elev); box-shadow: 0 20px 80px #0006; outline: none; }
header { display: flex; align-items: center; justify-content: space-between; }
.update-symbol { display: grid; place-items: center; width: 44px; height: 44px; border-radius: 12px; background: #4b83e620; color: var(--accent-hover); }
h2 { margin: 20px 0 8px; font-size: 22px; }
.version { margin: 0 0 22px; font-size: 16px; font-weight: 600; }
.version span { margin-left: 12px; font-size: 12px; color: var(--text-faint); font-weight: 400; }
.update-body { font-size: 13px; line-height: 1.6; }
details { padding: 12px; background: var(--bg); border-radius: 8px; }
summary { cursor: pointer; color: var(--text-dim); }
pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 180px; overflow: auto; font: inherit; margin: 12px 0 0; }
.download-status { margin-top: 18px; }
.download-status div { display: flex; justify-content: space-between; color: var(--text-dim); }
progress { width: 100%; height: 6px; accent-color: var(--accent); }
.ready { color: var(--green); }
.install-note { color: var(--text-faint); margin: 18px 0 0; }
.error { color: var(--red); margin-top: 12px; overflow-wrap: anywhere; }
.link { display: block; border: 0; background: transparent; color: var(--accent-hover); padding: 6px 0 0; }
.project-list { max-height: 180px; overflow: auto; padding-left: 20px; }
.danger { background: var(--red); color: white; }
footer { display: flex; justify-content: flex-end; gap: 10px; margin-top: 24px; }
</style>
