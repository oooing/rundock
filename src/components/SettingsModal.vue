<script setup lang="ts">
import { locale, setLocale, tr } from '@/i18n'

import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '@/api/http'
import type { ExportSnapshot } from '@/types'
import { getAppVersion, isTauri } from '@/tauri/window'
import AppUpdatePanel from './AppUpdatePanel.vue'
import MotionSettings from './MotionSettings.vue'
import { createAutoSettings, runtimeLimits } from '@/utils/autoSettings'
import { appUpdate } from '@/stores/appUpdate'

const emit = defineEmits<{ (e: 'close'): void }>()
const modal = ref<HTMLElement | null>(null)
let previousFocus: HTMLElement | null = null
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && !appUpdate.dialogOpen) { event.stopPropagation(); emit('close') }
}

const runtime = createAutoSettings(api)
const settings = runtime.state
const runtimeLabels = {
  grace_period_seconds: '停止等待时间（秒）',
  url_discover_timeout_seconds: '启动检测超时（秒）',
}
const appVersion = ref('0.1.0')

async function load() {
  const values = await runtime.load()
  if (values) closeRemembered.value = values.closeBehavior === 'minimize'
}

async function exportConfig() {
  const snap = await api.exportConfig()
  const blob = new Blob([JSON.stringify(snap, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `launcher-config-${new Date().toISOString().slice(0, 10)}.json`
  a.click()
  URL.revokeObjectURL(url)
}

function importConfig() {
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = '.json'
  input.onchange = async () => {
    const f = input.files?.[0]
    if (!f) return
    const text = await f.text()
    try {
      const snap = JSON.parse(text) as Partial<ExportSnapshot>
      const r = await api.importConfig(snap)
      alert(tr("导入完成：{0} 个应用，{1} 个分组", [r.apps, r.groups]))
      location.reload()
    } catch (e: any) {
      alert(tr("导入失败：") + (e?.message || e))
    }
  }
  input.click()
}

// 关闭行为记忆：当前是否「记住最小化」。重置 = 删除记忆，恢复每次询问。
const closeRemembered = ref(false)
const closeError = ref('')
async function resetCloseBehavior() {
  closeError.value = ''
  try {
    await api.setSettings({ closeBehavior: '' })
    closeRemembered.value = false
  } catch (error) { closeError.value = String(error) }
}

onMounted(() => {
  previousFocus = document.activeElement as HTMLElement | null
  modal.value?.focus()
  document.addEventListener('keydown', onKeydown)
  load()
  getAppVersion().then((v) => {
    appVersion.value = v
  })
})
onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <div ref="modal" class="modal" tabindex="-1" role="dialog" aria-modal="true" :aria-label="tr('设置')">
      <header class="m-head">
        <h2>{{ tr("设置") }}</h2>
        <button class="ghost icon" :aria-label="tr('关闭')" @click="emit('close')">✕</button>
      </header>

      <div class="m-body">
        <p class="autosave-note">{{ tr('设置自动保存，无需手动确认') }}</p>
        <AppUpdatePanel :current-version="appVersion" />

        <section class="block">
          <div class="row">
            <label for="ui-language">{{ tr('界面语言') }}</label>
            <select id="ui-language" :value="locale" @change="setLocale(($event.target as HTMLSelectElement).value)">
              <option value="zh-CN">简体中文</option>
              <option value="en">English</option>
            </select>
          </div>
        </section>

        <MotionSettings />

        <details class="block advanced">
          <summary>{{ tr('高级设置') }}<span>{{ tr('运行参数') }}</span></summary>
          <p class="desc">{{ tr('通常无需调整。修改后自动保存，用于下一次启动或停止项目。') }}</p>
          <p v-if="settings.loadError" class="setting-error" role="alert">{{ tr('设置加载失败：') }}{{ settings.loadError }} <button @click="load">{{ tr('重试') }}</button></p>
          <div v-for="(max, key) in runtimeLimits" :key="key" class="runtime-field">
            <div class="row">
              <label :for="key">{{ tr(runtimeLabels[key]) }}</label>
              <input :id="key" type="number" min="1" :max="max" step="1" :value="settings.values[key]" :disabled="!settings.loaded" :aria-invalid="['error', 'invalid'].includes(settings.fields[key].status)" :aria-describedby="`${key}-hint`" class="num" @input="runtime.set(key, ($event.target as HTMLInputElement).value)" />
            </div>
            <p :id="`${key}-hint`" class="desc">{{ key === 'grace_period_seconds' ? tr('给项目正常退出的时间，超时后强制停止。') : tr('等待启动服务就绪的时间，较慢的项目可适当增加。') }}</p>
            <p v-if="settings.fields[key].status === 'invalid'" class="setting-error" role="alert">{{ tr('请输入 1–{0} 的整数', [max]) }}</p>
            <p v-else-if="settings.fields[key].status === 'error'" class="setting-error" role="alert">{{ tr('未保存：') }}{{ settings.fields[key].error }} <button @click="runtime.set(key, settings.values[key])">{{ tr('重试') }}</button></p>
            <p v-else-if="settings.fields[key].status !== 'idle'" class="field-status" role="status">{{ settings.fields[key].status === 'saving' ? tr('保存中…') : tr('已自动保存') }}</p>
          </div>
        </details>

        <section class="block">
          <h4>{{ tr("配置导入导出") }}</h4>
          <p class="desc">{{ tr("导出全部应用、分组、设置为 JSON 文件，方便迁移到其它机器或团队共享。") }}</p>
          <div class="btn-row">
            <button @click="exportConfig">{{ tr("⬇ 导出配置") }}</button>
            <button @click="importConfig">{{ tr("⬆ 导入配置") }}</button>
          </div>
        </section>

        <section v-if="isTauri" class="block close-settings">
          <h4>{{ tr("关闭行为") }}</h4>
          <p class="desc">
            {{ closeRemembered ? tr("当前：关闭窗口时自动最小化到托盘（已记住）。") : tr("当前：关闭窗口时每次询问。") }}
          </p>
          <button v-if="closeRemembered" @click="resetCloseBehavior">{{ tr("恢复每次询问") }}</button>
          <p v-if="closeError" class="setting-error" role="alert">{{ tr('未保存：') }}{{ closeError }}</p>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
  padding: 20px;
}
.modal {
  outline: none;
  background: var(--bg-elev);
  border: 1px solid var(--border);
  border-radius: 14px;
  width: 100%;
  max-width: 540px;
  max-height: 88vh;
  display: flex;
  flex-direction: column;
}
.m-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}
.m-head h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}
.m-body {
  padding: 18px 20px;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 22px;
}
.block h4 {
  margin: 0 0 10px;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-faint);
}
.autosave-note { margin: 0; font-size: 12px; color: var(--text-faint); }
.advanced { border: 1px solid var(--border); border-radius: 10px; padding: 12px; }
.advanced summary { cursor: pointer; font-size: 13px; }
.advanced summary span { margin-left: 8px; color: var(--text-faint); font-size: 12px; }
.advanced[open] summary { margin-bottom: 16px; }
.runtime-field + .runtime-field { border-top: 1px solid var(--border); padding-top: 14px; margin-top: 14px; }
.runtime-field .row { margin-bottom: 6px; }
.runtime-field .desc { margin-bottom: 0; }
.field-status, .setting-error { font-size: 12px; margin: 6px 0 0; }
.field-status { color: var(--text-dim); }
.setting-error { color: var(--red); overflow-wrap: anywhere; }
.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
  gap: 12px;
}
.row label {
  font-size: 13px;
  color: var(--text-dim);
}
.num {
  width: 90px;
  text-align: right;
}
.desc {
  font-size: 12px;
  color: var(--text-faint);
  line-height: 1.6;
  margin: 0 0 10px;
}
.btn-row {
  display: flex;
  gap: 10px;
}
</style>
