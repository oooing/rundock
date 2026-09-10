<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '@/api/http'
import { getBaseURL } from '@/api/base'
import { wsClient } from '@/api/ws'
import { tr } from '@/i18n'
import UiIcon from './UiIcon.vue'
import type { AppView, CloudBuildStatus } from '@/types'

const props = defineProps<{ app?: AppView | null }>()
const emit = defineEmits<{ update: [alerts: CloudBuildStatus[]]; close: []; open: [appId: string] }>()
const alerts = ref<CloudBuildStatus[]>([])
const notices = ref<CloudBuildStatus[]>([])
const notice = computed(() => notices.value[0])
const noticed = new Set<string>()
let unsubscribe: (() => void) | undefined
const projectAlerts = computed(() => alerts.value.filter(alert => alert.appId === props.app?.id))
const errors = ref<Record<string, string>>({})
const busy = ref<string[]>([])
const dialog = ref<HTMLElement | null>(null)
let previousFocus: HTMLElement | null = null
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let revision = 0
let polling = false
let refreshQueued = false
function update(value: CloudBuildStatus[]) {
  alerts.value = value
  emit('update', value)
  // Only unread, actionable results arrive here. Success stays quiet, and the
  // same failure must not pop up again on each poll or WebSocket reconnect.
  notices.value = notices.value.filter(item => value.some(alert => alert.alertKey === item.alertKey))
  for (const alert of value) {
    if (!alert.alertKey || noticed.has(alert.alertKey)) continue
    noticed.add(alert.alertKey)
    notices.value.push(alert)
  }
}
function closeNotice() {
  notices.value.shift()
  try { sessionStorage.setItem(`rundock.cloud.noticed:${getBaseURL()}`, JSON.stringify([...noticed].filter(key => !notices.value.some(item => item.alertKey === key)))) } catch { /* In-memory deduplication still works. */ }
}
function viewNotice() {
  if (!notice.value) return
  emit('open', notice.value.appId)
  closeNotice()
}
async function poll() {
  if (disposed) return
  if (polling) { refreshQueued = true; return }
  if (timer) clearTimeout(timer)
  polling = true
  const startedRevision = revision
  try {
    const result = await api.cloudBuildAlerts()
    if (!disposed && startedRevision === revision) update(result || [])
  } catch { /* Retain known failures while the sidecar reconnects. */ }
  finally {
    polling = false
    if (!disposed) {
      if (refreshQueued) { refreshQueued = false; void poll() }
      else timer = setTimeout(poll, 15000)
    }
  }
}
async function dismiss(alert: CloudBuildStatus) {
  if (busy.value.includes(alert.alertKey)) return
  busy.value.push(alert.alertKey)
  errors.value[alert.alertKey] = ''
  try {
    await api.acknowledgeCloudBuild(alert.releaseRunId, alert.alertKey)
    revision++ // An older poll must not restore an acknowledged badge.
    update(alerts.value.filter(item => item.alertKey !== alert.alertKey))
    await nextTick()
    dialog.value?.querySelector<HTMLButtonElement>('.close-button')?.focus()
  } catch { errors.value[alert.alertKey] = tr('提醒未能关闭，请重试。') }
  finally { busy.value = busy.value.filter(key => key !== alert.alertKey) }
}
async function open(alert: CloudBuildStatus) {
  errors.value[alert.alertKey] = ''
  try { await api.openURL(alert.appId, alert.url) }
  catch { errors.value[alert.alertKey] = tr('未能打开 GitHub，请复制链接到浏览器查看。') }
}
function buildNumber(url: string) { return /\/actions\/runs\/(\d+)/.exec(url)?.[1] || '' }
function close() { emit('close') }
function trapFocus(event: KeyboardEvent) {
  if (event.key === 'Escape') { event.stopPropagation(); close(); return }
  if (event.key !== 'Tab') return
  const controls = dialog.value?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href]')
  if (!controls?.length) return
  const first = controls[0], last = controls[controls.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}
watch(() => props.app?.id, async id => {
  errors.value = {}
  if (id) { previousFocus = document.activeElement as HTMLElement; await nextTick(); dialog.value?.querySelector<HTMLButtonElement>('.close-button')?.focus() }
  else { previousFocus?.focus(); previousFocus = null }
})
function onVisible() { if (document.visibilityState === 'visible') void poll() }
onMounted(() => {
  try {
    const saved: unknown = JSON.parse(sessionStorage.getItem(`rundock.cloud.noticed:${getBaseURL()}`) || '[]')
    if (Array.isArray(saved)) saved.filter(key => typeof key === 'string').forEach(key => noticed.add(key))
  } catch { /* Storage may be disabled. */ }
  unsubscribe = wsClient.on(message => { if (message.type === 'cloud:build' || message.type === 'hello') void poll() })
  document.addEventListener('visibilitychange', onVisible)
  void poll()
})
onUnmounted(() => { disposed = true; unsubscribe?.(); document.removeEventListener('visibilitychange', onVisible); if (timer) clearTimeout(timer) })
</script>

<template>
  <Teleport to="body">
    <aside v-if="notice" class="build-notice" role="alert" aria-live="assertive" aria-atomic="true">
      <UiIcon name="alert-circle" :size="22" class="notice-icon" />
      <div class="notice-content">
        <strong>{{ notice.state === 'failed' ? tr('云端构建失败') : tr('云端构建需要关注') }}</strong>
        <p>{{ notice.appName }} · {{ notice.version }}</p>
        <p class="notice-summary">{{ tr(notice.summary) }}</p>
        <button @click="viewNotice">{{ tr('查看构建详情') }}<UiIcon name="arrow-right" :size="14" /></button>
      </div>
      <button class="close-button" :aria-label="tr('关闭提醒，保留卡片标识')" @click="closeNotice"><UiIcon name="close" :size="16" /></button>
    </aside>
    <div v-if="app" class="build-overlay" @click.self="close">
      <section ref="dialog" class="build-dialog" role="dialog" aria-modal="true" :aria-label="tr('{0} 的构建提醒', [app.name])" @keydown="trapFocus">
        <header class="build-heading"><div><p>{{ tr('云端构建提醒') }}</p><h2>{{ app.name }}</h2></div><button class="close-button" :aria-label="tr('关闭')" @click="close"><UiIcon name="close" /></button></header>
        <div class="build-body" aria-live="polite">
          <section v-for="alert in projectAlerts" :key="alert.alertKey" class="build-item" :class="{ failed: alert.state === 'failed' }">
            <div class="build-item-heading"><strong>{{ alert.version }}</strong><span><UiIcon name="alert-circle" :size="14" />{{ alert.state === 'failed' ? tr('构建失败') : tr('构建待确认') }}</span></div>
            <p v-if="buildNumber(alert.url)" class="build-number">GitHub Actions #{{ buildNumber(alert.url) }}</p>
            <p class="build-summary">{{ tr(alert.summary) }}</p>
            <div class="build-actions"><button v-if="alert.url" class="primary" @click="open(alert)">{{ tr('查看 GitHub 日志') }}<UiIcon name="external-link" :size="14" /></button><button :disabled="busy.includes(alert.alertKey)" @click="dismiss(alert)">{{ tr('标记已读') }}</button></div>
            <p v-if="errors[alert.alertKey]" class="build-error" role="alert">{{ errors[alert.alertKey] }} <a v-if="alert.url" :href="alert.url" target="_blank" rel="noopener noreferrer">{{ alert.url }}</a></p>
          </section>
          <p v-if="!projectAlerts.length" class="build-empty"><UiIcon name="check-circle" :size="24" />{{ tr('这个项目暂无未读构建提醒') }}</p>
        </div>
        <footer>{{ tr('关闭窗口会保留卡片标识；标记已读后清除对应提醒。') }}</footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.build-notice { position: fixed; right: 24px; bottom: 24px; z-index: 240; width: min(420px, calc(100vw - 48px)); box-sizing: border-box; display: flex; align-items: flex-start; gap: 12px; padding: 18px; color: var(--text); background: var(--bg-elev); border: 1px solid var(--border); border-left: 3px solid var(--red); border-radius: 12px; box-shadow: 0 12px 36px #0006; }
.notice-icon { color: var(--red); flex-shrink: 0; }.notice-content { flex: 1; min-width: 0; }.notice-content strong { font-size: 14px; }.notice-content p { margin: 7px 0; font-size: 13px; overflow-wrap: anywhere; }.notice-content .notice-summary { color: var(--text-dim); font-size: 12px; line-height: 1.6; max-height: 76px; overflow: auto; }.notice-content button { margin-top: 5px; padding: 0; border: 0; background: transparent; color: var(--accent); display: inline-flex; align-items: center; gap: 6px; font-size: 13px; }
.build-overlay { position: fixed; inset: 0; z-index: 250; padding: 24px; background: #0009; display: grid; place-items: center; }
.build-dialog { width: min(620px,100%); max-height: 85vh; display: flex; flex-direction: column; background: var(--bg-elev); color: var(--text); border: 1px solid var(--border); border-radius: 14px; box-shadow: 0 20px 60px #0006; }
.build-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 22px 24px; border-bottom: 1px solid var(--border); }.build-heading p { font-size: 12px; color: var(--text-dim); margin: 0 0 6px; }.build-heading h2 { font-size: 20px; margin: 0; overflow-wrap: anywhere; }.close-button { border: 0; background: transparent; padding: 4px; }
.build-body { overflow: auto; padding: 20px 24px; display: grid; gap: 14px; }.build-item { border: 1px solid var(--border); border-left: 3px solid var(--amber); border-radius: 10px; padding: 16px; background: var(--bg); }.build-item.failed { border-left-color: var(--red); }.build-item-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }.build-item-heading strong { font-size: 17px; overflow-wrap: anywhere; }.build-item-heading span { display: inline-flex; align-items: center; gap: 5px; color: var(--amber); font-size: 12px; white-space: nowrap; }.failed .build-item-heading span { color: var(--red); }.build-number { font-size: 12px; color: var(--text-dim); margin: 8px 0; }.build-summary { font-size: 13px; line-height: 1.7; color: var(--text-dim); overflow-wrap: anywhere; margin: 12px 0 16px; }.build-actions { display: flex; gap: 8px; flex-wrap: wrap; }.build-actions button { display: inline-flex; align-items: center; gap: 6px; }.build-error { font-size: 12px; color: var(--red); overflow-wrap: anywhere; }.build-empty { display: flex; align-items: center; justify-content: center; gap: 10px; padding: 28px 0; font-size: 14px; color: var(--text-dim); }footer { border-top: 1px solid var(--border); padding: 14px 24px; font-size: 12px; color: var(--text-dim); line-height: 1.6; }
</style>
