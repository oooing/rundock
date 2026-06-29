<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/http'
import type { ExportSnapshot } from '@/types'

const emit = defineEmits<{ (e: 'close'): void }>()

const settings = ref<Record<string, string>>({})
const saving = ref(false)
const saved = ref(false)

async function load() {
  settings.value = await api.getSettings()
}
async function save() {
  saving.value = true
  try {
    await api.setSettings(settings.value)
    saved.value = true
    setTimeout(() => (saved.value = false), 1500)
  } finally {
    saving.value = false
  }
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
      alert(`导入完成：${r.apps} 个应用，${r.groups} 个分组`)
      location.reload()
    } catch (e: any) {
      alert('导入失败：' + (e?.message || e))
    }
  }
  input.click()
}

// 关闭行为记忆：当前是否「记住最小化」。重置 = 删除记忆，恢复每次询问。
const closeRemembered = ref(false)
async function loadCloseBehavior() {
  try {
    const s = await api.getSettings()
    closeRemembered.value = s.closeBehavior === 'minimize'
  } catch {
    closeRemembered.value = false
  }
}
async function resetCloseBehavior() {
  await api.setSettings({ closeBehavior: '' })
  closeRemembered.value = false
}

onMounted(() => {
  load()
  loadCloseBehavior()
})
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <div class="modal">
      <header class="m-head">
        <h2>设置</h2>
        <button class="ghost icon" @click="emit('close')">✕</button>
      </header>

      <div class="m-body">
        <section class="block">
          <h4>运行参数</h4>
          <div class="row">
            <label>优雅停止等待（秒）</label>
            <input v-model="settings.grace_period_seconds" class="num" />
          </div>
          <div class="row">
            <label>URL 发现超时（秒）</label>
            <input v-model="settings.url_discover_timeout_seconds" class="num" />
          </div>
          <div class="row">
            <label>健康检查间隔（秒）</label>
            <input v-model="settings.health_check_interval_seconds" class="num" />
          </div>
          <div class="row">
            <label>每运行日志保留条数</label>
            <input v-model="settings.log_retention_per_run" class="num" />
          </div>
          <button class="primary" @click="save" :disabled="saving">
            {{ saving ? '保存中…' : saved ? '已保存 ✓' : '保存' }}
          </button>
        </section>

        <section class="block">
          <h4>配置导入导出</h4>
          <p class="desc">导出全部应用、分组、设置为 JSON 文件，方便迁移到其它机器或团队共享。</p>
          <div class="btn-row">
            <button @click="exportConfig">⬇ 导出配置</button>
            <button @click="importConfig">⬆ 导入配置</button>
          </div>
        </section>

        <section class="block">
          <h4>关闭行为</h4>
          <p class="desc">
            {{ closeRemembered ? '当前：关闭窗口时自动最小化到托盘（已记住）。' : '当前：关闭窗口时每次询问。' }}
          </p>
          <button v-if="closeRemembered" @click="resetCloseBehavior">恢复每次询问</button>
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
