<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/http'
import { useAppsStore } from '@/stores/apps'
import { tr } from '@/i18n'
import { isReplacementReady, restartConfirmationValid, type RestartPlan } from '@/utils/restart'

const props = defineProps<{ appId: string; appName: string }>()
const emit = defineEmits<{ (event: 'busy', value: boolean): void; (event: 'managed-restart'): void }>()
const store = useAppsStore()
const dialog = ref<HTMLDialogElement | null>(null)
const plan = ref<RestartPlan | null>(null)
const loading = ref(false)
const submitting = ref(false)
const message = ref('')
const error = ref('')
const expired = ref(false)
const done = ref(false)
let generation = 0
let disposed = false
let abort: AbortController | null = null
let timer: ReturnType<typeof setTimeout> | undefined
const busy = computed(() => loading.value || submitting.value)
const canConfirm = computed(() => !error.value && !expired.value && restartConfirmationValid(plan.value))
const canRenew = computed(() => !error.value && expired.value && plan.value?.canRestart && !!plan.value.confirmationToken)
watch(busy, value => emit('busy', value))

function invalidate() {
  generation++
  abort?.abort()
  clearTimeout(timer)
  plan.value = null
  loading.value = false
}
function close() {
  if (submitting.value) return
  dialog.value?.close()
  invalidate()
}
async function refresh() {
  if (busy.value || disposed) return
  invalidate()
  const current = generation, id = props.appId
  abort = new AbortController()
  loading.value = true
  error.value = ''; message.value = ''; done.value = false; expired.value = false
  try {
    const result = await api.restartPlan(id, abort.signal)
    if (disposed || current !== generation || id !== props.appId) return
    plan.value = result
    if (result.expiresAt) {
      const remaining = Date.parse(result.expiresAt) - Date.now()
      expired.value = !Number.isFinite(remaining) || remaining <= 0
      if (!expired.value) timer = setTimeout(() => { expired.value = true }, remaining)
    }
  } catch (cause) {
    if (!disposed && current === generation) error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    if (current === generation) loading.value = false
  }
}
async function open() {
  if (busy.value || store.operationBusy[props.appId]) return
  await nextTick()
  if (disposed || !dialog.value || dialog.value.open) return
  dialog.value.showModal()
  await refresh()
}
async function waitForBackend(instance: string) {
  const deadline = Date.now() + 90_000
  while (!disposed && Date.now() < deadline) {
    const controller = new AbortController()
    abort = controller
    const timeout = setTimeout(() => controller.abort(), 2000)
    try {
      if (isReplacementReady(instance, await api.restartHealth(controller.signal))) return
    } catch { /* Temporary disconnect during the requested restart. */ }
    finally { clearTimeout(timeout) }
    await new Promise(resolve => setTimeout(resolve, 800))
  }
  throw new Error(tr('尚未确认后台重启成功，请查看启动器日志。不会自动再次重启。'))
}
async function confirm() {
  if (!canConfirm.value || busy.value || store.operationBusy[props.appId]) return
  const id = props.appId, token = plan.value!.confirmationToken!
  plan.value = { ...plan.value!, confirmationToken: undefined }
  clearTimeout(timer)
  submitting.value = true; store.operationBusy[id] = true
  error.value = ''; message.value = tr('正在重启…')
  try {
    const result = await api.confirmRestart(id, token)
    if (result.restarting && result.instanceId) {
      message.value = tr('启动器正在重启 RunDock，等待后台重新连接…')
      await waitForBackend(result.instanceId)
    }
    await store.load()
    if (!disposed && props.appId === id) {
      done.value = true
      message.value = result.restarting ? tr('RunDock 后台已重启并重新连接。') : tr('项目已重新启动，正在等待服务就绪。')
    }
  } catch (cause) {
    if (!disposed && props.appId === id) {
      error.value = cause instanceof Error ? cause.message : String(cause)
      message.value = ''
    }
  } finally {
    delete store.operationBusy[id]
    submitting.value = false
  }
}
function managedRestart() { close(); emit('managed-restart') }
watch(() => props.appId, () => { dialog.value?.close(); invalidate() })
onBeforeUnmount(() => { disposed = true; invalidate(); dialog.value?.close() })
defineExpose({ open })
</script>

<template>
  <dialog ref="dialog" class="restart-dialog" :aria-labelledby="`restart-title-${appId}`" :aria-busy="busy" @cancel.prevent="close">
    <h2 :id="`restart-title-${appId}`">{{ tr('重启') }} · {{ appName }}</h2>
    <p v-if="loading || submitting" role="status" class="progress"><span class="spinner" aria-hidden="true"></span>{{ loading ? tr('正在核验进程归属和执行中的任务…') : message }}</p>
    <template v-else-if="!done && !error && plan">
      <p>{{ tr(plan.message) }}</p>
      <ul v-if="plan.processes?.length"><li v-for="process in plan.processes" :key="process.pid"><strong>{{ process.name }}</strong><span>PID {{ process.pid }}</span></li></ul>
      <p v-if="canRenew" role="status">{{ tr('确认信息已过期。更新后需再次确认，才会执行操作。') }}</p>
    </template>
    <p v-if="done" class="success" role="status">{{ message }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <footer>
      <button type="button" autofocus :disabled="submitting" @click="close">{{ done ? tr('完成') : busy || canConfirm || canRenew || (!error && plan?.kind === 'managed') ? tr('取消') : tr('关闭') }}</button>
      <button v-if="!busy && !done && canRenew" type="button" @click="refresh">{{ tr('更新确认信息') }}</button>
      <button v-if="!done && canConfirm" type="button" class="primary" :disabled="busy" @click="confirm">{{ plan?.kind === 'self' ? tr('重启 RunDock') : tr('确认重启') }}</button>
      <button v-if="plan?.kind === 'managed' && !busy && !done && !error" type="button" class="primary" @click="managedRestart">{{ tr('重启项目') }}</button>
    </footer>
  </dialog>
</template>

<style scoped>
.restart-dialog { width: min(480px, calc(100vw - 32px)); max-height: calc(100dvh - 48px); overflow: auto; margin: auto; padding: 20px; background: var(--bg-elev); color: var(--text); border: 1px solid var(--border); border-radius: 12px; box-shadow: var(--shadow); }
.restart-dialog::backdrop { background: rgb(0 0 0 / .6); }
h2 { margin: 0 0 16px; font-size: 17px; overflow-wrap: anywhere; }
p { font-size: 13px; line-height: 1.7; overflow-wrap: anywhere; }
ul { padding: 0; list-style: none; display: grid; gap: 6px; max-height: 240px; overflow: auto; }
li { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 6px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 6px; font-size: 12px; }
li strong { overflow-wrap: anywhere; } li span { color: var(--text-dim); }
.progress { display: flex; align-items: center; gap: 8px; color: var(--accent); }
.spinner { flex: 0 0 14px; height: 14px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: restart-spin .85s linear infinite; }
.error { color: var(--red); } .success { color: var(--green); }
footer { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; margin-top: 20px; }
button:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
@keyframes restart-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .spinner { animation: none; } }
</style>
