<script setup lang="ts">
import { tr } from '@/i18n'

import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
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
import ReleaseModal from '@/components/ReleaseModal.vue'
import { readReleaseSession, rememberReleaseSession } from '@/utils/releaseSession'
import { api } from '@/api/http'
import {
  hideMainWindow,
  isTauri as isTauriShell,
  onFileDragDrop,
  onTauriEvent,
  quitApp,
  showMainWindow,
} from '@/tauri/window'
import type { ImportCandidate, PendingOp } from '@/types'

const conn = useConnectionStore()
const apps = useAppsStore()
const groups = useGroupsStore()

const selectedGroupId = ref<string | null>(null)
const dropGroupId = ref<string | null>(null)
const movingGroups = ref<Record<string, boolean>>({})
const groupPickerOpen = ref(false)
const groupPickerRef = ref<HTMLElement | null>(null)
watch(groupPickerOpen, async (open) => {
  if (open) { await nextTick(); groupPickerRef.value?.focus() }
})
const selectedGroupName = computed(() => groups.groups.find(g => g.id === selectedGroupId.value)?.name || tr('未分组'))
const availableGroupApps = computed(() => apps.apps.filter(a => (a.groupId || '') !== selectedGroupId.value))
const showSettings = ref(false)
const showHelp = ref(false)
const candidate = ref<ImportCandidate | null>(null)
const importing = ref(false)
const logAppId = ref<string | null>(null)
const releaseAppId = ref<string | null>(readReleaseSession()?.appId || null)
watch(releaseAppId, (appId) => {
  const saved = readReleaseSession()
  rememberReleaseSession(appId ? (saved?.appId === appId ? saved : { appId }) : null)
}, { flush: 'sync' })
const dragOver = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)
const pathInput = ref('')
let unlistenFileDragDrop: (() => void) | null = null

// 启动/重启时脚本风险变化需确认：记下待执行的 op + appId + 最新候选。
// 与导入用的 candidate 互斥（不会同时出现，但语义独立，避免互相覆盖）。
const pending = ref<{ id: string; op: PendingOp; candidate: ImportCandidate } | null>(null)

// 窗口关闭行为：CloseDialog（最小化/退出选择）与 QuitConfirm（托盘退出二次确认）
const showCloseDialog = ref(false)
const showQuitConfirm = ref(false)
const isQuitting = ref(false)
// 记忆 key（复用 sidecar settings 表）
const CLOSE_BEHAVIOR_KEY = 'closeBehavior'

// 浏览器环境拿不到拖入文件的真实磁盘路径（File.path 仅 Tauri 有）。
// 用这个标志决定走哪条导入路径：Tauri 走拖放，浏览器走路径输入框。
const isTauri = !!(window as any).__TAURI_INTERNALS__ || !!(window as any).__TAURI__

const appsInGroup = computed(() => {
  if (selectedGroupId.value === null) return apps.apps
  return apps.apps.filter((a) => (a.groupId || '') === selectedGroupId.value)
})
const releaseApp = computed(() => apps.apps.find((a) => a.id === releaseAppId.value) || null)

function onDrop(paths: string[]) {
  const scripts = paths.filter((path) => /\.(bat|cmd|ps1)$/i.test(path))
  if (scripts.length === 0) {
    showToast(tr("请拖入 .bat、.cmd 或 .ps1 启动脚本"), 3500, 'warn')
    return
  }
  if (importing.value || candidate.value) {
    showToast(tr("请先完成当前脚本的导入确认"), 3500, 'warn')
    return
  }
  if (scripts.length > 1) {
    showToast(tr("一次导入一个脚本，已读取第一个文件"), 3000, 'info')
  }
  void importScript(scripts[0])
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
      pathInputHint.value = tr("请补全完整路径（含盘符，如 D:\\proj\\start.bat），然后回车或点导入")
    }
  }
  input.value = ''
}

function onContentDrop(e: DragEvent) {
  dragOver.value = false
  if (e.dataTransfer?.types.includes('application/x-projects-start-manager-card')) return
  const dt = e.dataTransfer
  if (dt?.files?.length) {
    const path = (dt.files[0] as any).path as string | undefined
    if (path) {
      onDrop([path])
    } else {
      // 浏览器拖放拿不到路径：提示改用输入框
      pathInputHint.value = tr("浏览器无法获取拖入文件的路径，请点「浏览」选择，或直接粘贴路径")
    }
  }
}

function onContentDragOver(e: DragEvent) {
  if (e.dataTransfer?.types.includes('application/x-projects-start-manager-card')) return
  dragOver.value = true
}

const pathInputHint = ref('')

function importFromInput() {
  const p = pathInput.value.trim().replace(/^["']|["']$/g, '')
  if (!p) return
  if (!isPathComplete(p)) {
    pathInputHint.value = tr("路径不完整。浏览器模式下「浏览」只能拿到文件名，请补全完整路径（含盘符），或直接粘贴完整路径。")
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
    showToast(tr("导入失败：") + (e?.message || e), 5000, 'error')
  } finally {
    importing.value = false
  }
}

async function confirmCreate() {
  if (!candidate.value) return
  try {
    await apps.createFromCandidate(candidate.value, selectedGroupId.value || undefined)
    showToast(tr("「{0}」已导入", [candidate.value.name]), 2500, 'success')
    candidate.value = null
  } catch (e: any) {
    showToast(tr("创建失败：") + (e?.message || e), 5000, 'error')
  }
}

async function handleReorder(order: string[]) {
  try {
    await apps.reorderCards(order)
  } catch (e: any) {
    showToast(tr("保存卡片顺序失败：") + (e?.message || e), 5000, 'error')
  }
}

async function handleMoveGroup(id: string, groupId: string) {
  const app = apps.apps.find(a => a.id === id)
  if (!app || movingGroups.value[id] || (app.groupId || '') === groupId) return
  if (groupId && !groups.groups.some(g => g.id === groupId)) return
  movingGroups.value[id] = true
  try {
    await apps.moveToGroup(id, groupId)
    const name = groups.groups.find(g => g.id === groupId)?.name || tr('未分组')
    showToast(tr('已将“{0}”移到“{1}”', [app.name, name]), 2500, 'success')
  } catch (e: any) {
    showToast(tr('移动分组失败：') + (e?.message || e), 5000, 'error')
  } finally {
    delete movingGroups.value[id]
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
  await quitWithNotice()
}

// 托盘右键「退出」菜单（Rust emit "tray-quit-requested"）—— 弹二次确认
async function onTrayQuitRequested() {
  showCloseDialog.value = false
  await showMainWindow()
  await nextTick()
  showQuitConfirm.value = true
}

async function onQuitConfirm() {
  await quitWithNotice()
}

async function quitWithNotice() {
  if (isQuitting.value) return
  showCloseDialog.value = false
  showQuitConfirm.value = false
  isQuitting.value = true
  await nextTick()
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
  try {
    await quitApp()
  } catch (e: any) {
    isQuitting.value = false
    showToast(tr("退出失败：") + (e?.message || e), 5000, 'error')
  }
}

// 置顶通知栏：成功/失败/提示 给明确反馈。
// kind: success(绿) / error(红) / info(蓝) / warn(琥珀)
type ToastKind = 'success' | 'error' | 'info' | 'warn'
interface Toast { id: number; msg: string; kind: ToastKind }
const toasts = ref<Toast[]>([])
let toastSeq = 0
let toastTimer: ReturnType<typeof setTimeout> | undefined
function showToast(msg: string, ms = 4000, kind: ToastKind = 'error') {
  const t: Toast = { id: ++toastSeq, msg, kind }
  toasts.value = [...toasts.value, t]
  // 超时自动移除（按 id 精确移除，避免多个 toast 互相干扰）
  setTimeout(() => dismissToast(t.id), ms)
}
function dismissToast(id: number) {
  toasts.value = toasts.value.filter((t) => t.id !== id)
}
function toastIcon(kind: ToastKind): string {
  return ({ success: '✓', error: '✕', info: 'ℹ', warn: '⚠' } as const)[kind]
}

async function handleStart(id: string) {
  const name = apps.apps.find(a => a.id === id)?.name || id
  try {
    const r = await apps.start(id)
    if (r.confirmation) {
      // 脚本风险变化：记下待执行操作，弹 ConfirmCard
      pending.value = { id, op: 'start', candidate: r.confirmation.candidate }
    } else {
      if (r.configUpdatedToast) showToast(tr("脚本配置已自动更新，正在启动…"), 3000, 'info')
      else showToast(tr("「{0}」已开始启动", [name]), 2500, 'success')
    }
  } catch (e: any) { showToast(tr("启动失败：") + (e?.message || e), 5000, 'error') }
}
async function handleStop(id: string) {
  const name = apps.apps.find(a => a.id === id)?.name || id
  try {
    await apps.stop(id)
    showToast(tr("「{0}」已停止", [name]), 2500, 'success')
  } catch (e: any) { showToast(tr("停止失败：") + (e?.message || e), 5000, 'error') }
}
async function handleRestart(id: string) {
  const name = apps.apps.find(a => a.id === id)?.name || id
  try {
    const r = await apps.restart(id)
    if (r.confirmation) {
      pending.value = { id, op: 'restart', candidate: r.confirmation.candidate }
    } else {
      if (r.configUpdatedToast) showToast(tr("脚本配置已自动更新，正在重启…"), 3000, 'info')
      else showToast(tr("「{0}」正在重启", [name]), 2500, 'success')
    }
  } catch (e: any) { showToast(tr("重启失败：") + (e?.message || e), 5000, 'error') }
}

// ConfirmCard（脚本变更确认）：用户确认 → 携带哈希重试原 start/restart 操作。
// 期间脚本若再次变化，后端会再次返回 409，这里把最新候选换上继续展示。
async function confirmPending() {
  const p = pending.value
  if (!p) return
  const name = apps.apps.find(a => a.id === p.id)?.name || p.id
  try {
    const r = await apps.resumeAfterConfirm(p.id, p.op, p.candidate.scriptHash)
    if (r.confirmation) {
      // 期间脚本又变了：更新候选，继续展示
      pending.value = { ...p, candidate: r.confirmation.candidate }
      return
    }
    pending.value = null
    if (r.configUpdatedToast) showToast(tr("脚本配置已自动更新，正在继续…"), 3000, 'info')
    else showToast(tr("「{0}」已{1}", [name, p.op === 'restart' ? tr("重启") : tr("启动")]), 2500, 'success')
  } catch (e: any) {
    showToast((p.op === 'restart' ? tr("重启") : tr("启动")) + tr("失败：") + (e?.message || e), 5000, 'error')
    pending.value = null
  }
}
function cancelPending() {
  pending.value = null
}

onMounted(async () => {
  conn.startPolling()
  apps.bindWS()
  // 监听 Tauri 壳事件：点 X 关闭、托盘右键退出
  void onTauriEvent('close-requested', onCloseRequested)
  void onTauriEvent('tray-quit-requested', onTrayQuitRequested)
  unlistenFileDragDrop = await onFileDragDrop((event) => {
    if (event.type === 'enter' || event.type === 'over') {
      dragOver.value = true
    } else if (event.type === 'leave') {
      dragOver.value = false
    } else if (event.type === 'drop') {
      dragOver.value = false
      onDrop(event.paths)
    }
  })
  // 托盘退出兜底：Rust 直接向 WebView 注入普通 DOM 事件，避免 Tauri 事件监听未注册时无响应。
  window.addEventListener('launcher-tray-quit-requested', () => {
    void onTrayQuitRequested()
  })
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

onUnmounted(() => {
  unlistenFileDragDrop?.()
  unlistenFileDragDrop = null
})
</script>

<template>
  <div class="layout">
    <GroupSidebar
      :selected="selectedGroupId"
      :drop-group-id="dropGroupId"
      @select="selectedGroupId = $event"
      @settings="showSettings = true"
    />

    <main class="main">
      <header class="topbar">
        <div class="title">
          <h1>{{ tr("项目启动器") }}</h1>
        </div>
        <div class="actions">
          <div class="import-box">
            <input
              v-model="pathInput"
              class="path-input"
              :class="{ incomplete: pathInput && !pathComplete }"
              :placeholder="isTauri ? tr('拖入脚本到下方，或点浏览') : tr('粘贴完整路径，如 D:\\proj\\start.bat')"
              @keyup.enter="importFromInput"
            />
            <button class="ghost" @click="fileInput?.click()" :title="tr('选择脚本文件')">{{ tr("浏览") }}</button>
            <button
              class="primary"
              :disabled="!pathInput.trim() || !pathComplete || importing"
              @click="importFromInput"
            >
              {{ importing ? tr("导入中…") : tr("导入") }}
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
            {{ tr("⚠ 只拿到文件名「") }}{{ pathInput }}{{ tr("」，浏览器无法自动获取磁盘路径。请补全完整路径（含盘符），例如在前面加上") }} <code>{{ tr("D:\\项目目录\\") }}</code>
          </div>
          <div v-else-if="pathInputHint" class="path-hint">{{ pathInputHint }}</div>
        </div>
      </header>

      <section
        class="content"
        :class="{ dragover: dragOver }"
        @dragover.prevent="onContentDragOver"
        @dragleave.prevent="dragOver = false"
        @drop.prevent="onContentDrop"
      >
        <div v-if="selectedGroupId !== null" class="group-toolbar">
          <strong>{{ selectedGroupName }}</strong>
          <button @click="groupPickerOpen = true" :disabled="!conn.sidecarReady">{{ tr('添加项目') }}</button>
        </div>
        <Dashboard
          :groups="groups.groups"
          :moving="movingGroups"
          :group-view="selectedGroupId !== null"
          @move-group="handleMoveGroup"
          @group-hover="dropGroupId = $event"
          :apps="appsInGroup"
          :loading="apps.loading"
          :ready="conn.sidecarReady"
          :connection-error="conn.error"
          @start="handleStart($event)"
          @stop="handleStop($event)"
          @restart="handleRestart($event)"
          @log="openLog($event)"
          @open-url="(id, url) => apps.openURL(id, url)"
          @open-dir="apps.openDir($event)"
          @release="releaseAppId = $event"
          @delete="apps.remove($event)"
          @rename="(id, name) => apps.rename(id, name)"
          @set-role="(appId, serviceId, role) => apps.setServiceRole(appId, serviceId, role)"
          @reidentify="(appId, serviceId) => apps.reidentifyService(appId, serviceId)"
          @set-color="(id, color) => apps.setCardColor(id, color)"
          @reorder="handleReorder"
          @import="importScript"
        />
      </section>
    </main>

    <ConfirmCard v-if="candidate" :candidate="candidate" @confirm="confirmCreate" @cancel="candidate = null" />
    <ConfirmCard
      v-if="pending"
      mode="script-change"
      :action="pending.op === 'restart' ? tr('重启') : tr('启动')"
      :candidate="pending.candidate"
      @confirm="confirmPending"
      @cancel="cancelPending"
    />
    <LogDrawer v-if="logAppId" :app-id="logAppId" @close="logAppId = null" />
    <ReleaseModal v-if="releaseApp" :app="releaseApp" @close="releaseAppId = null" />
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
    <div v-if="isQuitting" class="quitting-overlay">
      <div class="quitting-card">
        <div class="spinner"></div>
        <div>
          <div class="quitting-title">{{ tr("正在关闭服务并退出…") }}</div>
          <div class="quitting-sub">{{ tr("请稍等，正在停止所有正在运行的项目服务。") }}</div>
        </div>
      </div>
    </div>
    <div v-if="groupPickerOpen && selectedGroupId !== null" class="group-picker-overlay" @click.self="groupPickerOpen = false">
      <section ref="groupPickerRef" class="group-picker" role="dialog" aria-modal="true" tabindex="-1" :aria-label="tr('添加项目')" @keydown.esc="groupPickerOpen = false">
        <header><h2>{{ tr('添加到“{0}”', [selectedGroupName]) }}</h2><button :aria-label="tr('关闭')" @click="groupPickerOpen = false">✕</button></header>
        <div class="group-picker-list">
          <p v-if="!availableGroupApps.length">{{ tr('没有可添加的项目') }}</p>
          <div v-for="app in availableGroupApps" :key="app.id" class="group-picker-row">
            <span><strong>{{ app.name }}</strong><small>{{ groups.groups.find(g => g.id === app.groupId)?.name || tr('未分组') }}</small></span>
            <button :disabled="movingGroups[app.id]" @click="handleMoveGroup(app.id, selectedGroupId!)">{{ movingGroups[app.id] ? tr('保存中…') : tr('添加') }}</button>
          </div>
        </div>
      </section>
    </div>
    <!-- 置顶通知栏：成功/失败/提示 集中显示在顶部中央，可堆叠、可手动关闭 -->
    <div class="toast-stack">
      <transition-group name="toast">
        <div
          v-for="t in toasts"
          :key="t.id"
          class="toast"
          :class="t.kind"
          @click="dismissToast(t.id)"
        >
          <span class="toast-ico">{{ toastIcon(t.kind) }}</span>
          <span class="toast-msg">{{ t.msg }}</span>
          <button class="toast-x" :title="tr('关闭')" @click.stop="dismissToast(t.id)">✕</button>
        </div>
      </transition-group>
    </div>
  </div>
</template>

<style scoped>
.group-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 16px; }
.group-toolbar strong { min-width: 0; overflow-wrap: anywhere; }
.group-picker-overlay { position: fixed; inset: 0; z-index: 120; background: rgba(0,0,0,.55); display: grid; place-items: center; padding: 20px; }
.group-picker { width: min(520px,100%); max-height: 85vh; display: flex; flex-direction: column; border: 1px solid var(--border); border-radius: 14px; background: var(--bg-elev); }
.group-picker header { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 16px; }
.group-picker h2 { margin: 0; font-size: 16px; overflow-wrap: anywhere; }
.group-picker-list { padding: 0 16px 16px; overflow: auto; }
.group-picker-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 0; border-top: 1px solid var(--border); }
.group-picker-row span { min-width: 0; overflow-wrap: anywhere; }
.group-picker-row small { display: block; margin-top: 4px; color: var(--text-faint); }
.group-picker-row button { flex-shrink: 0; }
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
.actions {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 4px;
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
/* Longer translated labels must not push the import controls off-screen. */
@media (max-width: 1100px) {
  .topbar { flex-wrap: wrap; gap: 10px; }
  .title { flex-shrink: 0; }
  .title h1 { white-space: nowrap; }
  .actions, .import-box { width: 100%; min-width: 0; }
  .path-input { flex: 1; width: 0; min-width: 0; }
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
.quitting-overlay {
  position: fixed;
  inset: 0;
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.62);
}
.quitting-card {
  display: flex;
  align-items: center;
  gap: 14px;
  width: 360px;
  max-width: 88vw;
  padding: 18px;
  border: 1px solid var(--border);
  border-radius: 14px;
  background: var(--bg-elev);
}
.spinner {
  width: 22px;
  height: 22px;
  border: 3px solid rgba(255, 255, 255, 0.18);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  flex-shrink: 0;
}
.quitting-title {
  font-size: 15px;
  font-weight: 700;
}
.quitting-sub {
  margin-top: 4px;
  font-size: 12.5px;
  color: var(--text-dim);
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
/* ===== 置顶通知栏：成功/失败/提示 ===== */
.toast-stack {
  position: fixed;
  top: 16px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 400;
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: center;
  pointer-events: none;
  width: max-content;
  max-width: min(92vw, 560px);
}
.toast {
  pointer-events: auto;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  border-radius: 10px;
  font-size: 13px;
  font-weight: 500;
  color: #fff;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
  cursor: pointer;
  min-width: 280px;
  border: 1px solid rgba(255, 255, 255, 0.12);
}
.toast .toast-ico {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 700;
  background: rgba(255, 255, 255, 0.22);
}
.toast .toast-msg {
  flex: 1;
  line-height: 1.4;
  word-break: break-word;
}
.toast .toast-x {
  flex-shrink: 0;
  background: transparent;
  border: none;
  color: rgba(255, 255, 255, 0.7);
  cursor: pointer;
  font-size: 13px;
  padding: 2px 4px;
  border-radius: 4px;
  line-height: 1;
}
.toast .toast-x:hover {
  color: #fff;
  background: rgba(255, 255, 255, 0.15);
}
.toast.success { background: var(--green, #34d399); }
.toast.error   { background: var(--red, #f87171); }
.toast.info    { background: var(--accent, #4f8cff); }
.toast.warn    { background: var(--amber, #fbbf24); color: #1a1a1a; }
.toast.warn .toast-ico { background: rgba(0, 0, 0, 0.15); }
.toast.warn .toast-x { color: rgba(0, 0, 0, 0.55); }
.toast.warn .toast-x:hover { color: #000; background: rgba(0, 0, 0, 0.1); }

/* 进入/离开动画：从顶部滑入 */
.toast-enter-active,
.toast-leave-active {
  transition: all 0.28s cubic-bezier(0.2, 0.7, 0.3, 1);
}
.toast-enter-from {
  opacity: 0;
  transform: translateY(-16px) scale(0.96);
}
.toast-leave-to {
  opacity: 0;
  transform: translateY(-8px) scale(0.96);
}
.toast-move {
  transition: transform 0.28s cubic-bezier(0.2, 0.7, 0.3, 1);
}
</style>
