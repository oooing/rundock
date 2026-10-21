<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import { isTauri } from '@/tauri/window'
import { useAppsStore } from '@/stores/apps'
import UiIcon from './UiIcon.vue'
import StartupEntryPanel from './StartupEntryPanel.vue'
import './projectImport.css'
import type { ImportCandidate, StartupDiscovery } from '@/types'

const props = defineProps<{ initialPath?: string; groupId?: string; nativeDragOver?: boolean }>()
const emit = defineEmits<{ close: []; added: [name: string] }>()
const apps = useAppsStore()
const path = ref(props.initialPath || '')
const pathInput = ref<HTMLInputElement | null>(null)
const modal = ref<HTMLElement | null>(null)
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
const dragging = ref(false)
const dropHint = ref('')
const result = ref<StartupDiscovery | null>(null)
const selected = ref('')
const candidate = ref<ImportCandidate | null>(null)
const busy = ref(false)
const loadingEntry = ref(false)
const saving = ref(false)
const error = ref('')
const help = ref(false)
const detailsOpen = ref(false)
const composing = ref(false)
const errorPhase = ref<'discovery' | 'entry' | 'save'>('discovery')
let disposed = false
let discoveryRequest = 0
let entryRequest = 0
let debounceTimer: ReturnType<typeof setTimeout> | undefined
let discoveryTimer: ReturnType<typeof setTimeout> | undefined
let entryTimer: ReturnType<typeof setTimeout> | undefined
let discoveryAbort: AbortController | null = null
let entryAbort: AbortController | null = null
let pendingSource = ''
let detectedSource = ''
let backdropPointerId: number | null = null
let backdropClickReady = false
const hasDanger = computed(() => candidate.value?.findings?.some(finding => finding.level === 'danger'))
const cleanPath = computed(() => path.value.trim().replace(/^["']|["']$/g, ''))
const completePath = computed(() => /^(?:[a-z]:[\\/]|\/|\\\\)/i.test(cleanPath.value))
const normalize = (p: string) => p.replace(/\\/g, '/').replace(/\/$/, '').toLowerCase()
const existing = computed(() => candidate.value && apps.apps.find(app => normalize(app.entryScript) === normalize(candidate.value!.entryScript)))
const multipleEntries = computed(() => (result.value?.options.length || 0) > 1)
const recognizing = computed(() => busy.value || loadingEntry.value)
// Browsers can retarget click to the backdrop when an input selection ends outside.
// Dismiss only when both ends of the same primary gesture occur on the backdrop.
function beginBackdropGesture(event: PointerEvent) {
  backdropClickReady = false
  backdropPointerId = event.isPrimary && event.button === 0 && event.target === event.currentTarget ? event.pointerId : null
}
function endBackdropGesture(event: PointerEvent) {
  backdropClickReady = backdropPointerId === event.pointerId && event.target === event.currentTarget
  backdropPointerId = null
}
function resetBackdropGesture() { backdropPointerId = null; backdropClickReady = false }
function closeFromBackdrop() {
  const ready = backdropClickReady
  resetBackdropGesture()
  if (ready && !saving.value) emit('close')
}
function invalidateSource() {
  discoveryRequest++; entryRequest++
  clearTimeout(debounceTimer)
  clearTimeout(discoveryTimer); clearTimeout(entryTimer)
  discoveryTimer = undefined; entryTimer = undefined
  discoveryAbort?.abort(); entryAbort?.abort()
  discoveryAbort = null; entryAbort = null
  pendingSource = ''; detectedSource = ''
  selected.value = ''; candidate.value = null; result.value = null
  busy.value = false; loadingEntry.value = false; error.value = ''; help.value = false; detailsOpen.value = false
}
function scheduleRecognition() {
  clearTimeout(debounceTimer)
  if (!saving.value && !composing.value && completePath.value) debounceTimer = setTimeout(() => { void inspect() }, 400)
}
// Flush synchronously: an edited source cannot leave its old candidate eligible for saving.
watch(path, () => { invalidateSource(); scheduleRecognition() }, { flush: 'sync' })
function receiveDrop(paths: string[]) {
  dragging.value = false
  if (saving.value || !paths.length) return
  dropHint.value = paths.length > 1 ? tr('一次添加一个项目，已读取第一个路径。') : ''
  path.value = paths[0]
  void inspect()
}
async function handleDrop(event: DragEvent) {
  dragging.value = false
  if (saving.value) return
  const files = Array.from(event.dataTransfer?.files || [])
  const fullPaths = files.map(file => (file as File & { path?: string }).path).filter((p): p is string => !!p)
  if (fullPaths.length) { receiveDrop(fullPaths); return }
  if (!files.length && !event.dataTransfer?.types.includes('Files')) return
  path.value = ''
  invalidateSource()
  dropHint.value = tr('浏览器无法读取完整路径，请在下方粘贴文件或文件夹地址。')
  await nextTick()
  pathInput.value?.focus()
}
function enterPath() { pathInput.value?.focus(); pathInput.value?.select() }
function onEnter(event: KeyboardEvent) {
  if (event.isComposing || composing.value || event.keyCode === 229) return
  event.preventDefault()
  void inspect()
}
function endComposition() { composing.value = false; scheduleRecognition() }
function trapFocus(event: KeyboardEvent) {
  if (event.key !== 'Tab' || !modal.value) return
  const focusable = Array.from(modal.value.querySelectorAll<HTMLElement>('button, input, select, textarea, a[href], summary, [tabindex]'))
    .filter(element => element.tabIndex >= 0 && !element.matches(':disabled') && !element.closest('[inert]')
      && element.getClientRects().length > 0 && getComputedStyle(element).visibility !== 'hidden')
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (!first) { event.preventDefault(); modal.value.focus(); return }
  const active = document.activeElement
  if (!modal.value.contains(active) || active === modal.value || (event.shiftKey && active === first) || (!event.shiftKey && active === last)) {
    event.preventDefault()
    const target = event.shiftKey ? last : first
    target.focus()
  }
}
defineExpose({ receiveDrop })
async function inspect(force = false) {
  clearTimeout(debounceTimer)
  if (disposed || saving.value || composing.value || !completePath.value) return
  const source = cleanPath.value
  // Enter/drop may arrive during the debounce or after a completed scan; reuse that scan.
  if ((busy.value && pendingSource === source) || (!force && detectedSource === source && result.value)) return
  invalidateSource()
  const request = ++discoveryRequest
  const controller = new AbortController()
  discoveryAbort = controller
  pendingSource = source
  busy.value = true
  errorPhase.value = 'discovery'
  let timedOut = false
  const timeout = setTimeout(() => { timedOut = true; controller.abort() }, 15000)
  discoveryTimer = timeout
  try {
    const value = await api.discoverStartup(source, controller.signal)
    if (disposed || request !== discoveryRequest || cleanPath.value !== source) return
    result.value = value
    detailsOpen.value = value.options.length > 1
    detectedSource = source
    selected.value = value.options.find(item => item.recommended)?.path || (value.options.length === 1 ? value.options[0].path : '')
  } catch (cause: unknown) {
    if (!disposed && request === discoveryRequest && (!controller.signal.aborted || timedOut)) error.value = timedOut ? tr('识别超时，请重试。') : cause instanceof Error ? cause.message : String(cause)
  } finally {
    clearTimeout(timeout)
    if (!disposed && request === discoveryRequest) { busy.value = false; discoveryAbort = null; discoveryTimer = undefined; pendingSource = '' }
  }
}
async function loadEntry(entry: string) {
  const request = ++entryRequest
  const source = cleanPath.value
  entryAbort?.abort()
  clearTimeout(entryTimer)
  entryTimer = undefined
  candidate.value = null; error.value = ''; loadingEntry.value = !!entry
  if (!entry) return
  errorPhase.value = 'entry'
  const controller = new AbortController()
  entryAbort = controller
  let timedOut = false
  const timeout = setTimeout(() => { timedOut = true; controller.abort() }, 15000)
  entryTimer = timeout
  try {
    const value = await api.import(entry, controller.signal)
    if (!disposed && request === entryRequest && selected.value === entry && cleanPath.value === source) candidate.value = value
  } catch (cause: unknown) {
    if (!disposed && request === entryRequest && (!controller.signal.aborted || timedOut)) error.value = timedOut ? tr('识别超时，请重试。') : cause instanceof Error ? cause.message : String(cause)
  } finally {
    clearTimeout(timeout)
    if (!disposed && request === entryRequest) { loadingEntry.value = false; entryAbort = null; entryTimer = undefined }
  }
}
watch(selected, entry => { void loadEntry(entry) }, { flush: 'sync' })
function retry() { if (errorPhase.value === 'entry') void loadEntry(selected.value); else void inspect(true) }
async function save() {
  if (!candidate.value || busy.value || loadingEntry.value || saving.value || !candidate.value.name.trim() || existing.value) return
  saving.value = true; error.value = ''; errorPhase.value = 'save'
  try { const created = await apps.createFromCandidate(candidate.value, props.groupId); emit('added', created.name) }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) }
  finally { saving.value = false }
}
onMounted(() => { pathInput.value?.focus({ preventScroll: true }); if (props.initialPath) void inspect() })
watch(() => props.initialPath, value => {
  if (!value || saving.value || value === path.value) return
  path.value = value
  void inspect()
})
onUnmounted(() => { disposed = true; invalidateSource(); if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true }) })
</script>

<template>
  <div class="add-overlay project-import-layer" @pointerdown="beginBackdropGesture" @pointerup="endBackdropGesture" @pointercancel="resetBackdropGesture" @click.self="closeFromBackdrop" @dragover.prevent @drop.prevent.stop="handleDrop">
    <section ref="modal" class="add-modal project-import-panel" tabindex="-1" role="dialog" aria-modal="true" :aria-label="tr('添加项目')" @keydown="trapFocus" @keydown.esc="!saving && emit('close')">
      <header>
        <span class="header-icon"><UiIcon :name="result?.options.length ? 'log' : 'plus'" :size="24" /></span>
        <div><h2>{{ tr('添加项目') }}</h2><p>{{ result?.options.length ? tr('确认名称，即可添加。') : tr('拖入启动文件或项目文件夹。') }}</p></div>
        <button :aria-label="tr('关闭')" :disabled="saving" @click="emit('close')"><UiIcon name="close" /></button>
      </header>
      <div class="add-body">
        <section v-if="!path && !result" class="drop-zone" :class="{ dragging: dragging || nativeDragOver }" :aria-label="tr('拖入项目文件或文件夹')" @dragenter.prevent="dragging = true" @dragover.prevent="dragging = true" @dragleave.prevent="dragging = false" @drop.prevent.stop="handleDrop">
          <UiIcon name="folder" :size="24" /><span>{{ tr('拖入项目文件或文件夹') }}</span>
          <small>{{ isTauri ? '.bat · .cmd · .ps1' : tr('Web 版无法获取完整路径时，请使用下方输入框。') }}</small>
        </section>
        <p v-if="dropHint" class="drop-hint" role="status">{{ dropHint }}</p>
        <form class="path-form" @submit.prevent="inspect()">
          <label for="project-source-path">{{ tr('文件或文件夹完整路径') }}</label>
          <input id="project-source-path" ref="pathInput" v-model="path" :disabled="saving" placeholder="D:\Projects\my-app" autocomplete="off" spellcheck="false" @keydown.enter="onEnter" @compositionstart="composing = true" @compositionend="endComposition" />
        </form>
        <p v-if="busy || (!result && !error)" class="recognition-status" role="status">{{ busy ? tr('正在识别…') : tr('粘贴完整路径后自动识别。') }}</p>
        <div v-if="error && errorPhase !== 'entry'" class="import-error" role="alert"><span>{{ error }}</span><button v-if="errorPhase === 'discovery'" :disabled="recognizing || saving || !completePath" @click="retry">{{ tr('重试') }}</button></div>
        <section v-if="result && (!result.options.length || result.truncated)" class="startup-results">
          <div v-if="!result.options.length" class="not-found"><h3>{{ tr('暂未识别到启动方式') }}</h3><p>{{ tr('可以换一个文件或文件夹，或查看如何准备启动入口。') }}</p><div><button :disabled="recognizing || saving" @click="inspect(true)">{{ tr('重新识别') }}</button><button @click="enterPath">{{ tr('重新输入路径') }}</button><button @click="help = !help" :aria-expanded="help">{{ tr('查看准备方法') }}</button></div></div>
          <p v-if="result.truncated" class="muted">{{ tr('文件较多或部分目录无法读取，仅展示部分结果。可选择更具体的子文件夹继续识别。') }}</p>
        </section>
        <section v-if="result?.options.length === 1" class="entry-scope" :data-entry="result.options[0].path">
          <div class="entry-heading"><UiIcon name="log" :size="18" /><strong>{{ result.options[0].command || result.options[0].relativePath }}</strong><span v-if="candidate && !candidate.findings?.length" class="recognized"><UiIcon name="check" :size="14" />{{ tr('已识别') }}</span></div>
          <StartupEntryPanel :candidate="candidate" :loading="loadingEntry" :error="errorPhase === 'entry' ? error : ''" :duplicate="!!existing" :disabled="saving" collapsible @retry="retry" />
        </section>
        <details v-else-if="multipleEntries" class="startup-details" :open="detailsOpen" @toggle="detailsOpen = ($event.target as HTMLDetailsElement).open">
          <summary>{{ tr('启动详情') }}<span>{{ tr('选择一个启动方式') }}</span></summary>
          <fieldset class="startup-options" :disabled="saving">
            <legend class="sr-only">{{ tr('启动方式') }}</legend>
            <section v-for="option in result!.options" :key="option.path" class="entry-scope" :class="{ selected: selected === option.path }" :data-entry="option.path">
              <label class="entry-heading"><input v-model="selected" type="radio" name="startup-option" :value="option.path" /><span><strong>{{ option.command || option.relativePath }}</strong><small v-if="option.command">{{ option.relativePath }}</small></span><em v-if="option.recommended">{{ tr('推荐') }}</em></label>
              <StartupEntryPanel v-if="selected === option.path" :candidate="candidate" :loading="loadingEntry" :error="errorPhase === 'entry' ? error : ''" :duplicate="!!existing" :disabled="saving" @retry="retry" />
            </section>
          </fieldset>
        </details>
        <section v-if="candidate" class="import-details">
          <label for="import-app-name">{{ tr('应用名称') }}</label>
          <input id="import-app-name" v-model="candidate.name" :disabled="saving" />
        </section>
        <section v-if="help" class="preparation"><h3>{{ tr('如何准备启动入口') }}</h3><p>{{ tr('先查看项目说明中的“本地运行”或“快速开始”。') }}</p><p>{{ tr('Windows 项目可提供 .bat、.cmd 或 .ps1 启动脚本；Node 项目可在 package.json 中提供 dev、start 或 serve 命令。') }}</p><p>{{ tr('如果项目由 AI 或开发者提供，请让对方提供一个能够启动完整项目的脚本，再把脚本导入这里。') }}</p></section>
      </div>
      <footer><span>{{ tr('添加后不会自动启动') }}</span><button class="cancel" :disabled="saving" @click="emit('close')">{{ tr('取消') }}</button><button class="primary" :disabled="recognizing || saving || !candidate?.name.trim() || !!existing" @click="save">{{ saving ? tr('正在添加…') : hasDanger ? tr('我已知晓风险，确认导入') : tr('确认添加') }}</button></footer>
    </section>
  </div>
</template>

<style scoped>
.add-overlay { background: #0008; }
.add-modal { display: flex; flex-direction: column; }
header { display: flex; align-items: center; padding: 24px 24px 18px; gap: 12px; }
.header-icon { display: grid; place-items: center; flex: 0 0 42px; height: 42px; border-radius: 12px; color: var(--accent); background: color-mix(in srgb,var(--accent) 12%,var(--bg-elev)); }
h2 { font-size: 20px; margin: 0; letter-spacing: -.3px; }header p { margin: 5px 0 0; color: var(--text-dim); font-size: 12px; }
header>button { margin-left: auto; align-self: flex-start; border: 0; background: transparent; padding: 6px; color: var(--text-dim); }
.add-body { min-height: 0; overflow: auto; overscroll-behavior: contain; padding: 0 24px 24px; display: grid; gap: 16px; }
.path-form>label,.import-details>label { display: block; color: var(--text-dim); font-size: 12px; margin-bottom: 8px; }
.path-form>input,.import-details>input { width: 100%; height: 42px; }
.path-form>input { font-size: 12px; }.import-details>input { font-size: 15px; }
.entry-scope { min-width: 0; padding: 14px; border-radius: 9px; background: var(--bg); border: 1px solid var(--border); }
.entry-heading { display: flex; align-items: center; gap: 10px; min-width: 0; }
.entry-heading>.ui-icon { color: var(--text-dim); flex-shrink: 0; }.entry-heading strong { font-size: 13px; font-weight: 500; overflow-wrap: anywhere; }.entry-heading>span { min-width: 0; }
.recognized { display: flex; align-items: center; gap: 4px; margin-left: auto; flex-shrink: 0; font-size: 11px; color: var(--text-dim); }
.startup-details>summary { font-size: 13px; cursor: pointer; padding: 4px 0; }.startup-details>summary>span { margin-left: 10px; font-size: 12px; color: var(--text-dim); }
.startup-options { border: 0; padding: 0; margin: 12px 0 0; display: grid; gap: 8px; min-width: 0; }
.startup-options .entry-heading { cursor: pointer; }.entry-scope.selected { border-color: color-mix(in srgb,var(--accent) 55%,var(--border)); }
input[type=radio] { flex: 0 0 auto; accent-color: var(--accent); }.entry-heading small { display: block; margin-top: 5px; font-size: 12px; color: var(--text-dim); overflow-wrap: anywhere; }
em { color: var(--accent); font-size: 11px; font-style: normal; margin-left: auto; flex-shrink: 0; }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
.muted,.recognition-status { color: var(--text-dim); font-size: 12px; line-height: 1.6; margin: 0; }
.import-error { color: var(--red); font-size: 13px; line-height: 1.6; overflow-wrap: anywhere; }.import-error>button { margin-left: 8px; }
h3 { font-size: 14px; margin: 0; }.not-found p,.preparation p { font-size: 12px; color: var(--text-dim); line-height: 1.65; }.not-found button { margin: 4px 8px 4px 0; }
footer { display: flex; align-items: center; gap: 10px; padding: 16px 24px; border-top: 1px solid var(--border); }
footer span { margin-right: auto; font-size: 11px; color: var(--text-dim); }footer button { min-height: 38px; }footer .primary { min-width: 112px; }footer .cancel { background: transparent; border-color: transparent; color: var(--text-dim); }
button:focus-visible,input:focus-visible,summary:focus-visible,.add-modal:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
.drop-zone { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; min-height: 100px; padding: 18px; border: 1px dashed var(--border); border-radius: 10px; background: var(--bg); font-size: 13px; }
.drop-zone small { flex-basis: 100%; font-size: 11px; color: var(--text-dim); }.drop-zone>.ui-icon { color: var(--accent); }.drop-zone>* { pointer-events: none; }.drop-zone.dragging { border-color: var(--accent); }
.drop-hint { margin: 0; color: var(--accent); font-size: 12px; line-height: 1.6; }
@media(hover:hover) { footer .cancel:hover,header>button:hover { background: var(--bg-elev-2); color: var(--text); } }
@media(prefers-reduced-motion:no-preference) { button { transition: transform 150ms ease-out; }button:active:not(:disabled) { transform: scale(.96); } }
@media(max-width:600px) { header { padding: 20px 18px 16px; }.add-body { padding: 0 18px 20px; }footer { padding: 14px 18px; flex-wrap: wrap; }footer span { flex-basis: 100%; }footer .cancel { margin-left: auto; } }
</style>
