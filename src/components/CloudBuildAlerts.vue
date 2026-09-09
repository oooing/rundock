<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import UiIcon from './UiIcon.vue'
import type { AppView, CloudBuildStatus } from '@/types'

const props = defineProps<{ app?: AppView | null }>()
const emit = defineEmits<{ update: [alerts: CloudBuildStatus[]]; close: [] }>()
const alerts = ref<CloudBuildStatus[]>([])
const projectAlerts = computed(() => alerts.value.filter(alert => alert.appId === props.app?.id))
const errors = ref<Record<string, string>>({})
const busy = ref<string[]>([])
const dialog = ref<HTMLElement | null>(null)
let previousFocus: HTMLElement | null = null
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let revision = 0
function update(value: CloudBuildStatus[]) { alerts.value = value; emit('update', value) }
async function poll() {
  const startedRevision = revision
  try {
    const result = await api.cloudBuildAlerts()
    if (!disposed && startedRevision === revision) update(result || [])
  } catch { /* Retain known failures while the sidecar reconnects. */ }
  finally { if (!disposed) timer = setTimeout(poll, 15000) }
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
onMounted(poll)
onUnmounted(() => { disposed = true; if (timer) clearTimeout(timer) })
</script>

<template>
  <Teleport to="body">
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
.build-overlay { position: fixed; inset: 0; z-index: 250; padding: 24px; background: #0009; display: grid; place-items: center; }
.build-dialog { width: min(620px,100%); max-height: 85vh; display: flex; flex-direction: column; background: var(--bg-elev); color: var(--text); border: 1px solid var(--border); border-radius: 14px; box-shadow: 0 20px 60px #0006; }
.build-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 22px 24px; border-bottom: 1px solid var(--border); }.build-heading p { font-size: 12px; color: var(--text-dim); margin: 0 0 6px; }.build-heading h2 { font-size: 20px; margin: 0; overflow-wrap: anywhere; }.close-button { border: 0; background: transparent; padding: 4px; }
.build-body { overflow: auto; padding: 20px 24px; display: grid; gap: 14px; }.build-item { border: 1px solid var(--border); border-left: 3px solid var(--amber); border-radius: 10px; padding: 16px; background: var(--bg); }.build-item.failed { border-left-color: var(--red); }.build-item-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }.build-item-heading strong { font-size: 17px; overflow-wrap: anywhere; }.build-item-heading span { display: inline-flex; align-items: center; gap: 5px; color: var(--amber); font-size: 12px; white-space: nowrap; }.failed .build-item-heading span { color: var(--red); }.build-number { font-size: 12px; color: var(--text-dim); margin: 8px 0; }.build-summary { font-size: 13px; line-height: 1.7; color: var(--text-dim); overflow-wrap: anywhere; margin: 12px 0 16px; }.build-actions { display: flex; gap: 8px; flex-wrap: wrap; }.build-actions button { display: inline-flex; align-items: center; gap: 6px; }.build-error { font-size: 12px; color: var(--red); overflow-wrap: anywhere; }.build-empty { display: flex; align-items: center; justify-content: center; gap: 10px; padding: 28px 0; font-size: 14px; color: var(--text-dim); }footer { border-top: 1px solid var(--border); padding: 14px 24px; font-size: 12px; color: var(--text-dim); line-height: 1.6; }
</style>
