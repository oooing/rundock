<script setup lang="ts">
import { computed } from 'vue'
import { tr } from '@/i18n'
import { isTauri } from '@/tauri/window'
import { appUpdate as update, checkAppUpdate, openAppUpdate } from '@/stores/appUpdate'
defineProps<{ currentVersion: string }>()
const percent = computed(() => update.total ? Math.min(100, Math.floor(update.downloaded / update.total * 100)) : 0)
</script>

<template>
  <section class="app-update" :aria-label="tr('应用更新')">
    <div class="update-row">
      <div class="update-copy">
        <h3>{{ tr('应用更新') }} <span>v{{ currentVersion }}</span></h3>
        <p v-if="isTauri && update.info" class="available" role="status">
          {{ tr('新版本 v{0}', [update.info.version]) }}
          <span v-if="update.phase === 'downloading'"> · {{ tr('下载中 {0}%', [percent]) }}</span>
          <span v-else-if="update.phase === 'ready'"> · {{ tr('可安装') }}</span>
        </p>
        <p v-else-if="isTauri">{{ update.phase === 'current' ? tr('已是最新版本') : tr('启动时自动检查更新') }}</p>
        <p v-else>{{ tr('网页版 · 安装更新请使用桌面版') }}</p>
      </div>
      <button v-if="isTauri && update.info" :disabled="update.phase === 'installing'" @click="openAppUpdate">{{ update.phase === 'ready' ? tr('继续安装') : tr('查看更新') }}</button>
      <button v-else-if="isTauri" :disabled="update.phase === 'checking'" @click="checkAppUpdate">{{ update.phase === 'checking' ? tr('正在检查…') : tr('检查更新') }}</button>
      <a v-else href="https://github.com/oooing/rundock/releases" target="_blank" rel="noopener noreferrer">{{ tr('下载桌面版') }} ↗</a>
    </div>
    <p v-if="isTauri && update.error" class="update-error" role="alert">{{ tr('更新未完成：') }}{{ update.error }}</p>
  </section>
</template>

<style scoped>
.app-update { padding-bottom: 18px; border-bottom: 1px solid var(--border); }
.update-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.update-copy { min-width: 0; }
h3 { margin: 0; font-size: 14px; font-weight: 600; }
h3 span { margin-left: 6px; color: var(--text-dim); font-size: 12px; font-weight: 400; }
p { margin: 5px 0 0; color: var(--text-faint); font-size: 12px; line-height: 1.5; }
p.available { color: var(--green); }
button, a { flex-shrink: 0; font-size: 12px; }
a { color: var(--accent-hover); text-decoration: none; }
.update-error { color: var(--red); overflow-wrap: anywhere; }
</style>
