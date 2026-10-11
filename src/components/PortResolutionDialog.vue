<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/http'
import { useAppsStore } from '@/stores/apps'
import { tr } from '@/i18n'
import type { PortResolution } from '@/types'

const props = defineProps<{ appId: string; appName: string }>()
const emit = defineEmits<{ (event: 'busy', value: boolean): void; (event: 'start'): void }>()
const appsStore = useAppsStore()
const dialog = ref<HTMLDialogElement | null>(null)
const resolution = ref<PortResolution | null>(null)
const loading = ref(false)
const confirming = ref(false)
const temporaryStarting = ref(false)
const error = ref('')
const expired = ref(false)
let request = 0
let disposed = false
let planAbort: AbortController | null = null
let expiryTimer: ReturnType<typeof setTimeout> | undefined
const busy = computed(() => loading.value || confirming.value)
const hasAction = computed(() => !error.value && resolution.value?.state === 'conflict' && resolution.value.canResolve
  && !!resolution.value.confirmationToken && resolution.value.conflicts.length > 0
  && resolution.value.conflicts.every(conflict => conflict.canClose))
const canConfirm = computed(() => hasAction.value && !expired.value)
const temporaryContext = computed(() => resolution.value?.state === 'conflict' || resolution.value?.state === 'reserved')
const hasTemporaryAction = computed(() => !error.value && temporaryContext.value && resolution.value?.temporaryPort?.supported
  && !!resolution.value.temporaryPort.confirmationToken && !!resolution.value.temporaryPort.port)
const canStartTemporary = computed(() => hasTemporaryAction.value && !expired.value)
const canRenew = computed(() => (hasAction.value || hasTemporaryAction.value) && expired.value)
const resolutionMessage = computed(() => {
  const result = resolution.value
  if (hasTemporaryAction.value) return tr('原端口不可用。可以保留占用程序，临时使用其他端口启动。')
  if (result?.state === 'conflict' && !result.canResolve
    && result.message === '端口被其他程序占用，确认关闭后将自动启动项目') return tr('无法安全关闭占用程序。')
  return tr(result?.message || '')
})
watch(busy, value => emit('busy', value))

function invalidate() {
  request++
  planAbort?.abort()
  planAbort = null
  clearTimeout(expiryTimer)
  resolution.value = null
  error.value = ''
  loading.value = false
}
function close() {
  if (confirming.value) return
  dialog.value?.close()
  invalidate()
}
async function refresh() {
  if (busy.value || disposed) return
  const id = props.appId
  const current = ++request
  planAbort?.abort()
  const controller = new AbortController()
  planAbort = controller
  clearTimeout(expiryTimer)
  loading.value = true
  resolution.value = null
  error.value = ''
  expired.value = false
  try {
    const result = await api.portResolution(id, controller.signal)
    if (disposed || current !== request || id !== props.appId) return
    resolution.value = { ...result, conflicts: result.conflicts || [], reservedPorts: result.reservedPorts || [] }
    const deadlines = [result.expiresAt, result.temporaryPort?.expiresAt].filter((value): value is string => !!value)
    if (deadlines.length) {
      const remaining = Math.min(...deadlines.map(value => Date.parse(value))) - Date.now()
      expired.value = !Number.isFinite(remaining) || remaining <= 0
      if (!expired.value) expiryTimer = setTimeout(() => { expired.value = true }, Math.min(remaining, 2147483647))
    }
  } catch (cause: unknown) {
    if (!disposed && current === request && !controller.signal.aborted) error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    if (!disposed && current === request) { loading.value = false; planAbort = null }
  }
}
async function open() {
  if (busy.value || appsStore.operationBusy[props.appId]) return
  const id = props.appId
  invalidate()
  await nextTick()
  if (disposed || id !== props.appId || !dialog.value || dialog.value.open) return
  dialog.value.showModal()
  await refresh()
}
async function confirm(temporary = false) {
  if (!(temporary ? canStartTemporary.value : canConfirm.value) || busy.value) return
  const id = props.appId
  if (appsStore.operationBusy[id]) { error.value = tr('已有启停操作进行中，请稍后重试'); return }
  const token = temporary ? resolution.value!.temporaryPort!.confirmationToken! : resolution.value!.confirmationToken!
  // A submitted token is never reused: every retry requires a fresh plan and explicit confirmation.
  resolution.value = { ...resolution.value!, confirmationToken: undefined,
    temporaryPort: resolution.value!.temporaryPort ? { ...resolution.value!.temporaryPort, confirmationToken: undefined } : undefined }
  appsStore.operationBusy[id] = true
  temporaryStarting.value = temporary
  confirming.value = true
  error.value = ''
  try {
    if (temporary) await api.startTemporaryPort(id, token)
    else await api.resolvePorts(id, token)
    await appsStore.load()
    if (!disposed && id === props.appId) dialog.value?.close()
  } catch (cause: unknown) {
    if (!disposed && id === props.appId) error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    delete appsStore.operationBusy[id]
    confirming.value = false
    temporaryStarting.value = false
    if (!disposed && id === props.appId && dialog.value?.open) await appsStore.checkRuntime(id).catch(() => {})
  }
}
function start() {
  if (busy.value || appsStore.operationBusy[props.appId]) return
  close()
  emit('start')
}
function onCancel(event: Event) { event.preventDefault(); close() }
watch(() => props.appId, () => { dialog.value?.close(); invalidate() })
onBeforeUnmount(() => { disposed = true; invalidate(); dialog.value?.close() })
defineExpose({ open })
</script>

<template>
  <dialog ref="dialog" class="port-resolution-dialog" :aria-labelledby="`port-resolution-title-${appId}`" :aria-busy="busy" @cancel="onCancel" @close="!confirming && invalidate()">
    <h2 :id="`port-resolution-title-${appId}`">{{ tr('处理端口占用') }}</h2>
    <p class="project">{{ appName }}</p>
    <p v-if="confirming" role="status">{{ temporaryStarting ? tr('正在临时换端口启动；占用程序保持运行…') : tr('正在关闭占用程序并启动…') }}</p>
    <p v-else-if="loading" role="status">{{ tr('正在检查占用程序…') }}</p>
    <template v-else-if="resolution && !error">
      <p class="message">{{ resolutionMessage }}</p>
      <ul v-if="resolution.conflicts.length" class="programs">
        <li v-for="conflict in resolution.conflicts" :key="`${conflict.port}-${conflict.pid}`">
          <strong>{{ conflict.name || tr('未知进程') }}</strong>
          <span class="mono">PID {{ conflict.pid }} · :{{ conflict.port }}</span>
          <span v-if="conflict.managedAppName">{{ tr('项目：{0}', [conflict.managedAppName]) }}</span>
          <span v-if="!conflict.canClose && conflict.reason" class="blocked">{{ tr(conflict.reason) }}</span>
        </li>
      </ul>
      <p v-if="resolution.reservedPorts.length" class="mono">{{ tr('系统保留端口') }}: {{ resolution.reservedPorts.join('、') }}</p>
      <section v-if="temporaryContext" class="temporary-option" :class="{ supported: hasTemporaryAction }">
        <template v-if="hasTemporaryAction">
          <h3>{{ tr('保留占用程序，临时换端口') }}</h3>
          <p class="port-change mono">:{{ resolution.temporaryPort!.originalPort }} <span aria-hidden="true">→</span> :{{ resolution.temporaryPort!.port }}</p>
          <p>{{ tr('仅本次运行生效；不修改原配置，不停止占用程序。下次普通启动仍用原配置。') }}</p>
          <p class="storage-note">{{ tr('网页端口变化后，本地设置和收藏按不同来源隔离；旧数据不会删除。') }}</p>
        </template>
        <template v-else>
          <h3>{{ tr('暂不支持临时换端口') }}</h3>
          <p>{{ tr(resolution.temporaryPort?.reason || '当前后台未提供端口兼容信息；不会盲目更换端口。') }}</p>
        </template>
      </section>
      <p v-if="canConfirm" class="warning">{{ tr('选择“关闭占用程序并启动”会停止上面列出的程序，可能丢失未保存内容。') }}</p>
      <p v-if="canRenew" role="status">{{ tr('确认信息已过期。更新后需再次确认，才会执行操作。') }}</p>
    </template>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <footer>
      <button type="button" autofocus :disabled="confirming" @click="close">{{ confirming ? tr('处理中…') : loading || canConfirm || canRenew || (!error && resolution?.state === 'clear') ? tr('取消') : tr('关闭') }}</button>
      <button v-if="!busy && canRenew" type="button" @click="refresh">{{ tr('更新确认信息') }}</button>
      <button v-if="!busy && error" type="button" @click="refresh">{{ tr('重新查看可用方案') }}</button>
      <button v-if="canConfirm" type="button" :class="{ primary: !canStartTemporary }" :disabled="busy" @click="confirm(false)">{{ tr('关闭占用程序并启动') }}</button>
      <button v-if="canStartTemporary" type="button" class="primary" :disabled="busy" @click="confirm(true)">{{ tr('临时换空闲端口并启动') }}</button>
      <button v-else-if="resolution?.state === 'clear' && !error" type="button" class="primary" :disabled="busy" @click="start">{{ tr('启动项目') }}</button>
    </footer>
  </dialog>
</template>

<style scoped>
.port-resolution-dialog { width: min(460px, calc(100vw - 32px)); max-height: calc(100dvh - 48px); overflow: auto; margin: auto; padding: 20px; background: var(--bg-elev); color: var(--text); border: 1px solid var(--border); border-radius: 12px; box-shadow: var(--shadow); }
.port-resolution-dialog::backdrop { background: rgb(0 0 0 / .6); }
h2 { margin: 0; font-size: 18px; }.project { margin: 5px 0 16px; color: var(--text-dim); font-size: 13px; }
p { font-size: 13px; line-height: 1.6; }.message { margin: 12px 0; }
.programs { list-style: none; padding: 0; margin: 12px 0; display: grid; gap: 8px; }
.programs li { padding: 10px 12px; background: var(--bg); border: 1px solid var(--border); border-radius: 7px; display: grid; gap: 4px; font-size: 12px; overflow-wrap: anywhere; }
.programs strong { font-size: 13px; }.programs span { color: var(--text-dim); }
.warning, .blocked { color: var(--amber); }.error { color: var(--red); overflow-wrap: anywhere; }
.temporary-option { padding: 12px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg); }
.temporary-option.supported { border-color: var(--accent); }
.temporary-option h3 { margin: 0; font-size: 13px; }.temporary-option p { margin: 6px 0 0; }
.port-change { font-size: 18px; color: var(--accent); }.port-change span { color: var(--text-dim); }
.storage-note { color: var(--text-dim); font-size: 12px; }
footer { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 8px; margin-top: 20px; }
button:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
</style>
