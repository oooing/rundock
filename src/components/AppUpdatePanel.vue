<script setup lang="ts">
import { computed } from 'vue'
import { tr } from '@/i18n'
import { isTauri, openProjectReleases } from '@/tauri/window'
import { appUpdate as update, checkAppUpdate, downloadAppUpdate, installAppUpdate } from '@/stores/appUpdate'
defineProps<{ currentVersion: string }>()
const percent = computed(() => update.total ? Math.min(100, Math.floor(update.downloaded / update.total * 100)) : 0)
const busy = computed(() => ['checking', 'downloading', 'installing'].includes(update.phase))
async function openReleases(event: MouseEvent) {
  if (!isTauri) return
  event.preventDefault()
  try { await openProjectReleases() }
  catch { prompt(tr('未能打开 GitHub，请复制链接到浏览器查看。'), 'https://github.com/oooing/rundock/releases') }
}
</script>

<template>
  <section class="app-update" :aria-label="tr('应用更新')">
    <div class="update-heading"><div><h3>{{ tr('应用更新') }}</h3><span>{{ tr('当前版本') }} v{{ currentVersion }}</span></div><button v-if="isTauri" :disabled="busy" @click="checkAppUpdate">{{ update.phase === 'checking' ? tr('正在检查更新…') : tr('检查更新') }}</button></div>
    <template v-if="isTauri">
      <p class="auto-note">{{ tr('启动时自动检查并后台下载，确认安装后才会退出应用。') }}</p>
      <p v-if="update.phase === 'current'" role="status">{{ tr('当前已是最新可安装版本') }}</p>
      <template v-if="update.info">
        <strong class="new-version">v{{ currentVersion }} <span>→</span> v{{ update.info.version }}</strong>
        <details v-if="update.info.notes"><summary>{{ tr('更新说明') }}</summary><pre>{{ update.info.notes }}</pre></details>
        <div v-if="update.phase === 'downloading'" role="status"><progress :value="update.downloaded" :max="update.total || 1" :aria-label="tr('下载进度')"></progress><span>{{ tr('正在下载更新…') }} {{ percent }}%</span></div>
        <p v-if="update.phase === 'ready'">{{ tr('下载完成，安装包已校验') }}</p>
        <p v-if="update.phase === 'installing'" role="status">{{ tr('正在停止项目并打开安装程序…') }}</p>
        <p class="install-note">{{ tr('安装时将停止所有项目并退出 RunDock，项目和分组配置会保留。') }}</p>
        <button v-if="update.phase === 'available'" class="primary" @click="downloadAppUpdate">{{ tr('下载更新') }} · {{ (update.info.size / 1048576).toFixed(1) }} MB</button>
        <div v-if="update.phase === 'ready'" class="actions"><button class="primary" @click="installAppUpdate">{{ tr('退出并安装') }}</button><button @click="downloadAppUpdate">{{ tr('重新下载') }}</button></div>
      </template>
      <div v-if="update.error" class="update-error" role="alert">{{ update.error }}<a href="https://github.com/oooing/rundock/releases" target="_blank" rel="noopener noreferrer" @click="openReleases">{{ tr('查看版本下载页') }}</a></div>
    </template>
    <template v-else><p>{{ tr('应用内安装仅支持 Windows 桌面版，Web 版请下载安装包。') }}</p><a href="https://github.com/oooing/rundock/releases" target="_blank" rel="noopener noreferrer">{{ tr('查看版本下载页') }} ↗</a></template>
  </section>
</template>

<style scoped>
.app-update { padding: 16px; border: 1px solid var(--border); border-radius: 12px; background: var(--bg); }
.update-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }.update-heading h3 { margin: 0 0 6px; font-size: 15px; }.update-heading span,p,summary { color: var(--text-dim); font-size: 12px; line-height: 1.6; }.new-version { display: block; margin-top: 18px; font-size: 20px; }.new-version span { color: var(--text-faint); margin: 0 6px; }details { margin-top: 12px; }summary { cursor: pointer; }pre { white-space: pre-wrap; overflow-wrap: anywhere; font-family: inherit; font-size: 12px; max-height: 180px; overflow: auto; }progress { width: 100%; accent-color: var(--accent); margin-top: 16px; }.install-note { margin: 14px 0; }.actions { display: flex; gap: 8px; }a { color: var(--accent); font-size: 12px; }.update-error { color: var(--red); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; margin-top: 12px; }.update-error a { display: block; margin-top: 6px; }
</style>
