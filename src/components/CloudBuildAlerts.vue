<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import type { CloudBuildStatus } from '@/types'

const alerts = ref<CloudBuildStatus[]>([])
const error = ref('')
const busy = ref<string[]>([])
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
async function poll() {
  try {
    const result = await api.cloudBuildAlerts()
    if (!disposed) alerts.value = result || []
  } catch { /* Keep unread failures during a sidecar reconnect. */ }
  finally { if (!disposed) timer = setTimeout(poll, 15000) }
}
async function dismiss(alert: CloudBuildStatus) {
  if (busy.value.includes(alert.releaseRunId)) return
  busy.value.push(alert.releaseRunId)
  error.value = ''
  try {
    await api.acknowledgeCloudBuild(alert.releaseRunId, alert.alertKey)
    alerts.value = alerts.value.filter(item => item.alertKey !== alert.alertKey)
  } catch { error.value = tr('提醒未能关闭，请重试。') }
  finally { busy.value = busy.value.filter(id => id !== alert.releaseRunId) }
}
async function open(alert: CloudBuildStatus) {
  error.value = ''
  try { await api.openURL(alert.appId, alert.url) }
  catch { error.value = tr('未能打开 GitHub，请复制链接到浏览器查看。') }
}
onMounted(poll)
onUnmounted(() => { disposed = true; if (timer) clearTimeout(timer) })
</script>

<template>
  <aside v-if="alerts.length" class="cloud-alert-stack" :aria-label="tr('云端构建提醒')" aria-live="polite">
    <section v-for="alert in alerts" :key="alert.alertKey" class="cloud-alert" :class="{ failed: alert.state === 'failed' }" role="alert">
      <div class="cloud-alert-heading"><strong>{{ alert.state === 'failed' ? tr('云端构建失败') : tr('云端构建状态待确认') }}</strong><button :disabled="busy.includes(alert.releaseRunId)" :aria-label="tr('关闭提醒')" @click="dismiss(alert)">✕</button></div>
      <div class="cloud-alert-project">{{ alert.appName }} <span>{{ alert.version }}</span></div>
      <p>{{ tr(alert.summary) }}</p>
      <button class="cloud-alert-link" @click="open(alert)">{{ tr('查看 GitHub 日志') }} ↗</button>
      <span v-if="error" class="cloud-alert-error">{{ error }} <a :href="alert.url" target="_blank" rel="noopener noreferrer">{{ alert.url }}</a></span>
    </section>
  </aside>
</template>

<style scoped>
.cloud-alert-stack { position: fixed; z-index: 250; right: 22px; bottom: 22px; display: grid; gap: 12px; width: min(400px,calc(100vw - 28px)); max-height: 65vh; overflow: auto; }
.cloud-alert { padding: 16px; border: 1px solid var(--border); border-left: 4px solid var(--amber); border-radius: 12px; background: var(--bg-elev); color: var(--text); box-shadow: 0 10px 32px #0006; }
.cloud-alert.failed { border-left-color: var(--red); }
.cloud-alert-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.cloud-alert-heading strong { font-size: 15px; }.cloud-alert-heading button { padding: 3px 7px; border: 0; background: transparent; color: var(--text-dim); }
.cloud-alert-project { margin-top: 10px; font-weight: 600; font-size: 13px; overflow-wrap: anywhere; }.cloud-alert-project span { margin-left: 8px; color: var(--text-dim); font-weight: 400; }
.cloud-alert p { margin: 8px 0 12px; color: var(--text-dim); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.cloud-alert-link { color: var(--accent); font-size: 12px; }.cloud-alert-error { display: block; margin-top: 8px; color: var(--red); font-size: 12px; overflow-wrap: anywhere; }
@media(max-width:600px) { .cloud-alert-stack { right: 14px; bottom: 14px; } }
</style>
