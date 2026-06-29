<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useConnectionStore } from '@/stores/connection'
import { useAppsStore } from '@/stores/apps'
import { useGroupsStore } from '@/stores/groups'
import GroupSidebar from '@/components/GroupSidebar.vue'
import Dashboard from '@/views/Dashboard.vue'
import ConfirmCard from '@/components/ConfirmCard.vue'
import LogDrawer from '@/components/LogDrawer.vue'
import SettingsModal from '@/components/SettingsModal.vue'
import HelpModal from '@/components/HelpModal.vue'
import CloseDialog from '@/components/CloseDialog.vue'
import QuitConfirm from '@/components/QuitConfirm.vue'
import { api } from '@/api/http'
import { hideMainWindow, isTauri as isTauriShell, onTauriEvent, quitApp } from '@/tauri/window'
import type { ImportCandidate } from '@/types'

const conn = useConnectionStore()
const apps = useAppsStore()
const groups = useGroupsStore()

const selectedGroupId = ref<string | null>(null)
const showSettings = ref(false)
const showHelp = ref(false)
const candidate = ref<ImportCandidate | null>(null)
const importing = ref(false)
const logAppId = ref<string | null>(null)
const dragOver = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)
const pathInput = ref('')

// 窗口关闭行为：CloseDialog（最小化/退出选择）与 QuitConfirm（托盘退出二次确认）
const showCloseDialog = ref(false)
const showQuitConfirm = ref(false)
// 记忆 key（复用 sidecar settings 表）
const CLOSE_BEHAVIOR_KEY = 'closeBehavior'

// 浏览器环境拿不到拖入文件的真实磁盘路径（File.path 仅 Tauri 有）。
// 用这个标志决定走哪条导入路径：Tauri 走拖放，浏览器走路径输入框。
const isTauri = !!(window as any).__TAURI_INTERNALS__ || !!(window as any).__TAURI__

const appsInGroup = computed(() => {
  if (selectedGroupId.value === null) return apps.apps
  return apps.apps.filter((a) => a.groupId === selectedGroupId.value)
})

function onDrop(paths: string[]) {
  if (paths.length === 0) return
  void importScript(paths[0])
}

// 浏览器文件选择：拿不到完整路径，只能用文件名回填到输入框，由用户补全目录。
// Tauri 下 File.path 有效，直接导入。
function onFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  const f = input.files?.[0]
  if (f) {
    const path = (f as any).path as string | undefined
    if (path) {
      // Tauri 环境：直接拿到完整磁盘路径
      pathInput.value = path
      importScript(path)
    } else if (f.name) {
      // 浏览器：只能拿到文件名，回填并提示补全
      pathInput.value = f.name
      pathInputHint.value = '请补全完整路径（含盘符，如 D:\\proj\\start.bat），然后回车或点导入'
    }
  }
  input.value = ''
}

function onContentDrop(e: DragEvent) {
  dragOver.value = false
  const dt = e.dataTransfer
  if (dt?.files?.length) {
    const path = (dt.files[0] as any).path as string | undefined
    if (path) {
      onDrop([path])
    } else {
      // 浏览器拖放拿不到路径：提示改用输入框
      pathInputHint.value = '浏览器无法获取拖入文件的路径，请点「浏览」选择，或直接粘贴路径'
    }
  }
}

const pathInputHint = ref('')

function importFromInput() {
  const p = pathInput.value.trim().replace(/^["']|["']$/g, '')
  if (!p) return
  if (!isPathComplete(p)) {
    pathInputHint.value = '路径不完整。浏览器模式下「浏览」只能拿到文件名，请补全完整路径（含盘符），或直接粘贴完整路径。'
    return
  }
  pathInputHint.value = ''
  pathInput.value = ''
  void importScript(p)
}

// 路径是否完整（含盘符或为绝对路径）。浏览器 file input 只给文件名，需此校验。
function isPathComplete(p: string): boolean {
  return /^[a-zA-Z]:[\\/]/.test(p) || p.startsWith('/')
}
const pathComplete = computed(() => isPathComplete(pathInput.value.trim().replace(/^["']|["']$/g, '')))

async function importScript(scriptPath: string) {
  importing.value = true
  try {
    candidate.value = await apps.importRaw(scriptPath)
  } catch (e: any) {
    alert('导入失败：' + (e?.message || e))
  } finally {
    importing.value = false
  }
}

async function confirmCreate() {
  if (!candidate.value) return
  try {
    await apps.createFromCandidate(candidate.value)
    candidate.value = null
  } catch (e: any) {
    alert('创建失败：' + (e?.message || e))
  }
}

function openLog(id: string) {
  logAppId.value = id
}

// ===== 窗口关闭 / 托盘退出处理 =====

// 读取关闭行为记忆（sidecar settings 表）。返回 'minimize' 表示记住最小化，其它为每次询问。
async function readCloseBehavior(): Promise<string> {
  try {
    const s = await api.getSettings()
    return s[CLOSE_BEHAVIOR_KEY] || ''
  } catch {
    return ''
  }
}

// 点 X 关闭时（Rust prevent_close + emit "close-requested"）。
async function onCloseRequested() {
  if (!isTauriShell) return
  const behavior = await readCloseBehavior()
  if (behavior === 'minimize') {
    // 记住了最小化，直接隐藏
    await hideMainWindow()
  } else {
    // 每次询问：弹选择框
    showCloseDialog.value = true
  }
}

// CloseDialog：选「最小化到托盘」—— 隐藏窗口并记住选择
async function onCloseMinimize() {
  showCloseDialog.value = false
  await hideMainWindow()
  try {
    await api.setSettings({ [CLOSE_BEHAVIOR_KEY]: 'minimize' })
  } catch {
    /* 记忆失败不影响隐藏 */
  }
}

// CloseDialog：选「退出」—— 不记忆，直接退出（CloseDialog 已说明会停服务）
async function onCloseQuit() {
  showCloseDialog.value = false
  await quitApp()
}

// 托盘右键「退出」菜单（Rust emit "tray-quit-requested"）—— 弹二次确认
function onTrayQuitRequested() {
  showQuitConfirm.value = true
}

async function onQuitConfirm() {
  showQuitConfirm.value = false
  await quitApp()
}

onMounted(async () => {
  conn.startPolling()
  apps.bindWS()
  // 监听 Tauri 壳事件：点 X 关闭、托盘右键退出
  void onTauriEvent('close-requested', onCloseRequested)
  void onTauriEvent('tray-quit-requested', onTrayQuitRequested)
  // 快捷键：? 显示帮助，Esc 关闭
  window.addEventListener('keydown', (e: KeyboardEvent) => {
    // 在输入框/确认卡里不触发，避免误触
    const tag = (e.target as HTMLElement)?.tagName
    const inField = tag === 'INPUT' || tag === 'TEXTAREA'
    if ((e.key === '?' || (e.key === '/' && e.shiftKey)) && !inField) {
      showHelp.value = !showHelp.value
    }
    if (e.key === 'Escape') {
      showHelp.value = false
    }
  })
  // 等就绪后加载
  const wait = setInterval(async () => {
    if (conn.sidecarReady) {
      clearInterval(wait)
      await Promise.all([apps.load(), groups.load()])
    }
  }, 500)
})
</script>

<template>
  <div class="layout">
    <GroupSidebar
      :selected="selectedGroupId"
      @select="selectedGroupId = $event"
      @settings="showSettings = true"
    />

    <main class="main">
      <header class="topbar">
        <div class="title">
          <span class="logo">⚡</span>
          <h1>启动平台</h1>
          <button class="ghost icon help-btn" title="使用说明（快捷键 ?）" @click="showHelp = true">?</button>
          <span class="conn" :class="{ ok: conn.sidecarReady }">
            {{ conn.sidecarReady ? '已连接' : '等待 sidecar…' }}
          </span>
        </div>
        <div class="actions">
          <div class="import-box">
            <input
              v-model="pathInput"
              class="path-input"
              :class="{ incomplete: pathInput && !pathComplete }"
              :placeholder="isTauri ? '拖入脚本到下方，或点浏览' : '粘贴完整路径，如 D:\\proj\\start.bat'"
              @keyup.enter="importFromInput"
            />
            <button class="ghost" @click="fileInput?.click()" title="选择脚本文件">浏览</button>
            <button
              class="primary"
              :disabled="!pathInput.trim() || !pathComplete || importing"
              @click="importFromInput"
            >
              {{ importing ? '导入中…' : '导入' }}
            </button>
            <input
              ref="fileInput"
              type="file"
              accept=".bat,.cmd,.ps1"
              style="display: none"
              @change="onFileChange"
            />
          </div>
          <div v-if="pathInput && !pathComplete" class="path-hint warn">
            ⚠ 只拿到文件名「{{ pathInput }}」，浏览器无法自动获取磁盘路径。请补全完整路径（含盘符），例如在前面加上 <code>D:\项目目录\</code>
          </div>
          <div v-else-if="pathInputHint" class="path-hint">{{ pathInputHint }}</div>
        </div>
      </header>

      <section
        class="content"
        :class="{ dragover: dragOver }"
        @dragover.prevent="dragOver = true"
        @dragleave.prevent="dragOver = false"
        @drop.prevent="onContentDrop"
      >
        <Dashboard
          :apps="appsInGroup"
          :loading="apps.loading"
          :ready="conn.sidecarReady"
          @start="apps.start($event)"
          @stop="apps.stop($event)"
          @restart="apps.restart($event)"
          @log="openLog($event)"
          @open-url="(id, url) => apps.openURL(id, url)"
          @open-dir="apps.openDir($event)"
          @delete="apps.remove($event)"
          @rename="(id, name) => apps.rename(id, name)"
          @set-role="(appId, serviceId, role) => apps.setServiceRole(appId, serviceId, role)"
          @reidentify="(appId, serviceId) => apps.reidentifyService(appId, serviceId)"
          @import="importScript"
        />
      </section>
    </main>

    <ConfirmCard v-if="candidate" :candidate="candidate" @confirm="confirmCreate" @cancel="candidate = null" />
    <LogDrawer v-if="logAppId" :app-id="logAppId" @close="logAppId = null" />
    <SettingsModal v-if="showSettings" @close="showSettings = false" />
    <HelpModal v-if="showHelp" @close="showHelp = false" />
    <CloseDialog
      v-if="showCloseDialog"
      @minimize="onCloseMinimize"
      @quit="onCloseQuit"
      @close="showCloseDialog = false"
    />
    <QuitConfirm
      v-if="showQuitConfirm"
      @confirm="onQuitConfirm"
      @cancel="showQuitConfirm = false"
    />
  </div>
</template>

<style scoped>
.layout {
  display: flex;
  height: 100vh;
  overflow: hidden;
}
.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 20px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-elev);
}
.title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.logo {
  font-size: 20px;
}
.title h1 {
  font-size: 16px;
  font-weight: 600;
  margin: 0;
}
.conn {
  font-size: 12px;
  color: var(--amber);
  padding: 2px 8px;
  border-radius: 10px;
  background: rgba(251, 191, 36, 0.12);
}
.conn.ok {
  color: var(--green);
  background: rgba(52, 211, 153, 0.12);
}
.actions {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 4px;
}
.help-btn {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  padding: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-dim);
  flex-shrink: 0;
  line-height: 1;
}
.help-btn:hover {
  color: var(--accent);
  border-color: var(--accent);
}
.import-box {
  display: flex;
  gap: 6px;
  align-items: center;
}
.path-input {
  width: 320px;
  font-size: 12.5px;
}
.path-input.incomplete {
  border-color: var(--amber);
  background: rgba(251, 191, 36, 0.06);
}
.path-hint {
  font-size: 11px;
  color: var(--text-faint);
  max-width: 520px;
  text-align: right;
  line-height: 1.5;
}
.path-hint.warn {
  color: var(--amber);
}
.path-hint code {
  background: var(--bg-elev-2);
  padding: 0 4px;
  border-radius: 3px;
}
.content {
  flex: 1;
  overflow: auto;
  padding: 20px;
  position: relative;
  transition: background 0.15s;
}
.content.dragover {
  background: rgba(79, 140, 255, 0.06);
  outline: 2px dashed var(--accent);
  outline-offset: -10px;
}
</style>
