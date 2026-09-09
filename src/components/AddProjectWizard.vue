<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import { isTauri } from '@/tauri/window'
import { useAppsStore } from '@/stores/apps'
import UiIcon from './UiIcon.vue'
import type { ImportCandidate, StartupDiscovery } from '@/types'

const props = defineProps<{ initialPath?: string; groupId?: string; nativeDragOver?: boolean }>()
const emit = defineEmits<{ close: []; added: [name: string] }>()
const apps = useAppsStore()
const path = ref(props.initialPath || '')
const pathInput = ref<HTMLInputElement | null>(null)
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
let disposed = false
let entryRequest = 0
const hasDanger = computed(() => candidate.value?.findings?.some(finding => finding.level === 'danger'))
const cleanPath = computed(() => path.value.trim().replace(/^["']|["']$/g, ''))
const completePath = computed(() => /^(?:[a-z]:[\\/]|\/|\\\\)/i.test(cleanPath.value))
const normalize = (p: string) => p.replace(/\\/g, '/').replace(/\/$/, '').toLowerCase()
const existing = computed(() => candidate.value && apps.apps.find(app => normalize(app.entryScript) === normalize(candidate.value!.entryScript)))
function receiveDrop(paths: string[]) {
  dragging.value = false
  if (busy.value || saving.value || !paths.length) return
  dropHint.value = paths.length > 1 ? tr('一次添加一个项目，已读取第一个路径。') : ''
  path.value = paths[0]
  void inspect()
}
async function handleDrop(event: DragEvent) {
  dragging.value = false
  if (busy.value || saving.value) return
  const files = Array.from(event.dataTransfer?.files || [])
  const fullPaths = files.map(file => (file as File & { path?: string }).path).filter((p): p is string => !!p)
  if (fullPaths.length) { receiveDrop(fullPaths); return }
  if (!files.length && !event.dataTransfer?.types.includes('Files')) return
  editPath()
  path.value = ''
  dropHint.value = tr('浏览器无法读取完整路径，请在下方粘贴文件或文件夹地址。')
  await nextTick()
  pathInput.value?.focus()
}
function editPath() { entryRequest++; selected.value = ''; candidate.value = null; loadingEntry.value = false; result.value = null; error.value = ''; help.value = false }
function enterPath() { editPath(); pathInput.value?.focus(); pathInput.value?.select() }
defineExpose({ receiveDrop })
async function inspect() {
  if (busy.value || saving.value || !completePath.value) return
  editPath()
  busy.value = true
  try {
    const value = await api.discoverStartup(cleanPath.value)
    if (!disposed) { result.value = value; selected.value = value.options.find(item => item.recommended)?.path || (value.options.length === 1 ? value.options[0].path : '') }
  } catch (e) { if (!disposed) error.value = e instanceof Error ? e.message : String(e) }
  finally { busy.value = false }
}
watch(selected, async entry => {
  const request = ++entryRequest
  candidate.value = null; error.value = ''; loadingEntry.value = !!entry
  if (!entry) return
  try { const value = await api.import(entry); if (!disposed && request === entryRequest) candidate.value = value }
  catch (e) { if (!disposed && request === entryRequest) error.value = e instanceof Error ? e.message : String(e) }
  finally { if (request === entryRequest) loadingEntry.value = false }
})
async function save() {
  if (!candidate.value || busy.value || loadingEntry.value || saving.value || !candidate.value.name.trim() || existing.value) return
  saving.value = true; error.value = ''
  try { const created = await apps.createFromCandidate(candidate.value, props.groupId); emit('added', created.name) }
  catch (e) { error.value = e instanceof Error ? e.message : String(e) }
  finally { saving.value = false }
}
onMounted(() => { if (props.initialPath) void inspect() })
onUnmounted(() => { disposed = true })
</script>

<template>
  <div class="add-overlay" @click.self="!busy && !saving && emit('close')" @dragover.prevent @drop.prevent.stop="handleDrop">
    <section class="add-modal" role="dialog" aria-modal="true" :aria-label="tr('添加项目')" @keydown.esc="!busy && !saving && emit('close')">
      <header><h2>{{ tr('添加项目') }}</h2><button :aria-label="tr('关闭')" :disabled="busy || saving" @click="emit('close')"><UiIcon name="close" /></button></header>
      <div class="add-body">
        <section class="drop-zone" :class="{ dragging: dragging || nativeDragOver, busy: busy || saving, compact: !!result }" :aria-label="tr('拖入项目文件或文件夹')" :aria-busy="busy" @dragenter.prevent="dragging = true" @dragover.prevent="dragging = true" @dragleave.prevent="dragging = false" @drop.prevent.stop="handleDrop">
          <UiIcon name="folder" :size="32" />
          <h3>{{ busy ? tr('正在识别…') : tr('拖入项目文件或文件夹') }}</h3>
          <p>{{ tr('启动脚本或整个项目文件夹，拖到这里即可识别。') }}</p>
          <small>{{ isTauri ? '.bat · .cmd · .ps1' : tr('Web 版无法获取完整路径时，请使用下方输入框。') }}</small>
        </section>
        <p v-if="dropHint" class="drop-hint" role="status">{{ dropHint }}</p>
        <div class="input-divider"><span>{{ tr('或直接输入路径') }}</span></div>
        <form class="path-form" @submit.prevent="inspect">
          <div class="path-row"><input id="project-source-path" ref="pathInput" v-model="path" :aria-label="tr('文件或文件夹完整路径')" :disabled="busy || saving" placeholder="D:\Projects\my-app" @input="editPath" /><button type="submit" :disabled="busy || saving || !completePath">{{ busy ? tr('正在识别…') : tr('识别启动方式') }}</button></div>
        </form>
        <div v-if="error" class="import-error" role="alert">{{ error }}</div>
        <section v-if="result" class="startup-results" aria-live="polite">
          <template v-if="result.options.length">
            <h3>{{ tr('启动方式') }}</h3>
            <div class="startup-options" role="radiogroup" :aria-label="tr('启动方式')"><label v-for="option in result.options" :key="option.path" :class="{ selected: selected === option.path }"><input v-model="selected" type="radio" name="startup-option" :value="option.path" :disabled="busy || saving" /><span><strong>{{ option.command || option.relativePath }} <em v-if="option.recommended">{{ tr('推荐') }}</em></strong><small>{{ option.kind === 'package' ? option.relativePath : tr('运行这个启动脚本') }}</small></span></label></div>
          </template>
          <div v-else class="not-found"><UiIcon name="folder" :size="28" /><h3>{{ tr('暂未识别到启动方式') }}</h3><p>{{ tr('可以换一个文件或文件夹，或查看如何准备启动入口。') }}</p><div><button @click="enterPath">{{ tr('重新输入路径') }}</button><button @click="help = !help" :aria-expanded="help">{{ tr('查看准备方法') }}</button></div></div>
          <p v-if="result.truncated" class="muted">{{ tr('文件较多或部分目录无法读取，仅展示部分结果。可选择更具体的子文件夹继续识别。') }}</p>
        </section>
        <p v-if="loadingEntry" class="muted" role="status">{{ tr('正在识别…') }}</p>
        <section v-if="candidate" class="import-details">
          <label for="import-app-name">{{ tr('应用名称') }}</label>
          <input id="import-app-name" v-model="candidate.name" :disabled="saving" />
          <details class="advanced" :key="candidate.entryScript">
            <summary>{{ tr('高级设置') }}</summary>
            <dl>
              <dt>{{ tr('入口脚本') }}</dt><dd>{{ candidate.entryScript }}</dd>
              <dt>{{ tr('工作目录') }}</dt><dd>{{ candidate.cwd }}</dd>
              <dt>{{ tr('适配器') }}</dt><dd>{{ candidate.adapterType }}</dd>
              <dt>{{ tr('启动命令') }}</dt><dd>{{ candidate.cmd }} {{ candidate.args.join(' ') }}</dd>
              <dt>{{ tr('端口提示') }}</dt><dd>{{ candidate.portHints.join(', ') || tr('无') }}</dd>
              <template v-if="candidate.markers?.length"><dt>{{ tr('项目标志') }}</dt><dd>{{ candidate.markers.join(', ') }}</dd></template>
              <template v-for="(value, key) in candidate.env" :key="key"><dt>{{ key }}</dt><dd>{{ value }}</dd></template>
            </dl>
          </details>
          <div v-if="candidate.findings?.length" class="findings">
            <h3>{{ tr('风险扫描结果') }}</h3>
            <p v-for="(finding, index) in candidate.findings" :key="index" :class="finding.level">{{ finding.message }}<code v-if="finding.snippet">{{ finding.snippet }}</code></p>
          </div>
          <p v-if="existing" class="import-error" role="alert">{{ tr('这个启动入口已经添加，可直接使用现有项目卡片。') }}</p>
        </section>
        <section v-if="help" class="preparation"><h3>{{ tr('如何准备启动入口') }}</h3><p>{{ tr('先查看项目说明中的“本地运行”或“快速开始”。') }}</p><p>{{ tr('Windows 项目可提供 .bat、.cmd 或 .ps1 启动脚本；Node 项目可在 package.json 中提供 dev、start 或 serve 命令。') }}</p><p>{{ tr('如果项目由 AI 或开发者提供，请让对方提供一个能够启动完整项目的脚本，再把脚本导入这里。') }}</p></section>
      </div>
      <footer><span>{{ tr('添加后生成项目卡片，点击“启动”才会运行。') }}</span><button :disabled="busy || saving" @click="emit('close')">{{ tr('取消') }}</button><button class="primary" :disabled="busy || loadingEntry || saving || !candidate?.name.trim() || !!existing" @click="save">{{ saving ? tr('正在添加…') : hasDanger ? tr('我已知晓风险，确认导入') : tr('确认添加') }}</button></footer>
    </section>
  </div>
</template>

<style scoped>
.add-overlay { position: fixed; inset: 0; z-index: 100; background: #0009; display: grid; place-items: center; padding: 20px; }
.add-modal { width: min(720px,100%); max-height: 90vh; display: flex; flex-direction: column; background: var(--bg-elev); border: 1px solid var(--border); border-radius: 16px; box-shadow: 0 20px 60px #0006; }
header { display: flex; justify-content: space-between; align-items: flex-start; padding: 22px 24px; border-bottom: 1px solid var(--border); gap: 16px; }h2 { font-size: 20px; margin: 0; }header p { margin: 6px 0 0; color: var(--text-dim); font-size: 13px; }header>button { border: 0; background: transparent; padding: 4px; }
.add-body { overflow: auto; padding: 20px 24px; display: grid; gap: 20px; }
.startup-options label { display: flex; align-items: center; gap: 12px; padding: 16px; border: 1px solid var(--border); border-radius: 10px; background: var(--bg); cursor: pointer; }.startup-options label.selected { border-color: var(--accent); background: color-mix(in srgb,var(--accent) 9%,var(--bg)); }input[type=radio] { flex: 0 0 auto; accent-color: var(--accent); }label span { min-width: 0; }label strong { display: block; font-size: 14px; overflow-wrap: anywhere; }label small { display: block; margin-top: 5px; font-size: 12px; color: var(--text-dim); overflow-wrap: anywhere; }
.path-form>label { display: block; font-size: 13px; margin-bottom: 8px; }.path-row { display: flex; gap: 8px; }.path-row input { flex: 1; min-width: 0; }.path-row button { white-space: nowrap; }.muted { color: var(--text-dim); font-size: 12px; line-height: 1.6; margin: 8px 0 0; }h3 { margin: 0; font-size: 15px; }.startup-options { margin-top: 12px; display: grid; gap: 8px; max-height: 270px; overflow: auto; }em { display: inline-block; color: var(--accent); font-size: 11px; font-weight: 500; font-style: normal; margin-left: 6px; }.import-error { color: var(--red); background: color-mix(in srgb,var(--red) 8%,var(--bg)); padding: 12px; border-radius: 8px; font-size: 13px; }.not-found { text-align: center; border: 1px dashed var(--border); padding: 24px; border-radius: 12px; }.not-found h3 { margin-top: 10px; }.not-found p,.preparation p { font-size: 13px; color: var(--text-dim); line-height: 1.65; }.not-found button+button { margin-left: 8px; }.preparation { padding: 16px; background: var(--bg); border-radius: 10px; }
footer { display: flex; align-items: center; gap: 10px; padding: 16px 24px; border-top: 1px solid var(--border); }footer span { margin-right: auto; font-size: 12px; color: var(--text-dim); }button:focus-visible,input:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
@media(max-width:600px) { header,.add-body,footer { padding: 16px; }.path-row { flex-wrap: wrap; }.path-row input { flex-basis: 100%; }footer { flex-wrap: wrap; }footer span { flex-basis: 100%; } }
.drop-zone { display: flex; align-items: center; flex-direction: column; justify-content: center; gap: 12px; min-height: 190px; padding: 28px 20px; text-align: center; border: 1px dashed var(--border); border-radius: 12px; background: var(--bg); transition: border-color .15s, background .15s; }
.drop-zone.compact { min-height: 0; padding: 14px; gap: 6px; }.drop-zone.compact>.ui-icon,.drop-zone.compact>small { display: none; }.drop-zone.compact h3 { font-size: 14px; }
.import-details { display: grid; gap: 10px; }.import-details>label { font-size: 14px; font-weight: 600; }.advanced { margin-top: 4px; }.advanced summary { font-size: 13px; cursor: pointer; color: var(--text-dim); }.advanced dl { display: grid; grid-template-columns: 90px minmax(0,1fr); gap: 8px 12px; font-size: 12px; padding: 12px; background: var(--bg); border-radius: 8px; }.advanced dt { color: var(--text-dim); overflow-wrap: anywhere; }.advanced dd { margin: 0; overflow-wrap: anywhere; font-family: monospace; }.findings p { font-size: 12px; line-height: 1.6; padding: 10px; background: var(--bg); border-radius: 8px; overflow-wrap: anywhere; }.findings code { display: block; }.findings .danger { color: var(--red); }.findings .warn { color: var(--amber); }
.drop-zone>.ui-icon { color: var(--accent); }.drop-zone h3 { font-size: 18px; }.drop-zone p { color: var(--text-dim); font-size: 13px; margin: 0; }.drop-zone small { color: var(--text-faint); font-size: 12px; }.drop-zone.dragging { border-color: var(--accent); background: color-mix(in srgb,var(--accent) 12%,var(--bg)); }.drop-zone>* { pointer-events: none; }.drop-zone.busy { opacity: .65; }.drop-hint { margin: 0; color: var(--accent); font-size: 13px; line-height: 1.6; }.input-divider { display: flex; align-items: center; gap: 14px; color: var(--text-dim); font-size: 12px; }.input-divider::before,.input-divider::after { content: ''; height: 1px; flex: 1; background: var(--border); }
</style>
