import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { localBuildApi, LocalBuildError } from '@/api/localBuild'
import { tr } from '@/i18n'
import type { LocalBuildPreparation, LocalBuildRequest, LocalBuildRun, LocalBuildView } from '@/types/localBuild'

const isRunning = (run: LocalBuildRun) => run.status === 'queued' || run.status === 'running'
const messageOf = (error: unknown) => error instanceof Error ? error.message : tr('操作失败，请重试')

export function useLocalBuilds(appId: () => string) {
  const preparation = ref<LocalBuildPreparation | null>(null)
  const recentRuns = ref<LocalBuildRun[]>([])
  const selectedIds = ref<string[]>([])
  const view = ref<LocalBuildView | null>(null)
  const pendingRequest = ref<LocalBuildRequest | null>(null)
  const loading = ref(false)
  const submitting = ref(false)
  const cancelling = ref(false)
  const readingRun = ref(false)
  const error = ref('')
  const runError = ref('')
  const actionError = ref('')
  const cancelError = ref('')
  let generation = 0
  let listRevision = 0
  let viewRevision = 0
  let disposed = false
  let pollTimer: ReturnType<typeof setTimeout> | undefined
  const controllers = new Set<AbortController>()
  const active = computed(() => !!view.value && isRunning(view.value.run))
  const existingActive = computed(() => recentRuns.value.find(isRunning))
  const busy = computed(() => submitting.value || cancelling.value)
  const canStart = computed(() => !loading.value && !busy.value && !active.value && !existingActive.value
    && !pendingRequest.value && !!preparation.value?.configFingerprint && selectedIds.value.length > 0)

  function storageKey(kind: 'pending' | 'view') { return `rundock.local-build.${kind}.${appId()}` }
  function readStorage(kind: 'pending' | 'view') {
    try { return localStorage.getItem(storageKey(kind)) || '' } catch { return '' }
  }
  function writeStorage(kind: 'pending' | 'view', value: string) {
    try {
      if (value) localStorage.setItem(storageKey(kind), value)
      else localStorage.removeItem(storageKey(kind))
    } catch { /* Server history remains authoritative when browser storage is unavailable. */ }
  }
  function restorePending(): LocalBuildRequest | null {
    try {
      const value = JSON.parse(readStorage('pending')) as LocalBuildRequest
      return /^[a-f\d-]{36}$/i.test(value.requestId) && typeof value.configFingerprint === 'string'
        && Array.isArray(value.targetIds) && value.targetIds.length > 0
        && value.targetIds.every(id => typeof id === 'string') ? value : null
    } catch { return null }
  }
  function requestScope(timeout = 20000) {
    const controller = new AbortController()
    controllers.add(controller)
    const timer = setTimeout(() => controller.abort(), timeout)
    return { signal: controller.signal, release: () => { clearTimeout(timer); controllers.delete(controller) } }
  }
  function stopPolling() { if (pollTimer) clearTimeout(pollTimer); pollTimer = undefined }
  function resetRequests() {
    generation++
    listRevision++
    viewRevision++
    stopPolling()
    for (const controller of controllers) controller.abort()
    controllers.clear()
  }
  function pollAgain() {
    stopPolling()
    if (!disposed && active.value && !runError.value) pollTimer = setTimeout(() => void refreshRun(), 1200)
  }

  async function load(restore = false) {
    const current = generation
    const revision = ++listRevision
    const id = appId()
    const scope = requestScope()
    loading.value = true
    error.value = ''
    try {
      const data = await localBuildApi.list(id, scope.signal)
      if (disposed || current !== generation || revision !== listRevision) return
      preparation.value = data.preparation ? { ...data.preparation, targets: data.preparation.targets || [] } : null
      error.value = data.preparationError ? tr(data.preparationError)
        : preparation.value ? '' : tr('本地构建配置暂不可用，请在设置中检查。')
      recentRuns.value = data.recentRuns || []
      const available = preparation.value?.targets.filter(target => target.available) || []
      selectedIds.value = selectedIds.value.filter(id => available.some(target => target.id === id))
      if (!selectedIds.value.length && available.length) selectedIds.value = [available[0].id]
      if (restore && !pendingRequest.value) {
        const run = recentRuns.value.find(isRunning)
          || recentRuns.value.find(run => run.id === readStorage('view'))
        if (run) void showRun(run)
      }
    } catch (reason) {
      if (!disposed && current === generation && revision === listRevision) {
        error.value = scope.signal.aborted ? tr('读取超时，请重试。') : messageOf(reason)
        const rememberedRun = readStorage('view')
        // Saved output is tied to its frozen run, not to today's editable configuration.
        if (restore && !pendingRequest.value && !view.value && /^[a-z\d-]{1,80}$/i.test(rememberedRun)) {
          void refreshRun(rememberedRun)
        }
      }
    } finally {
      scope.release()
      if (!disposed && current === generation && revision === listRevision) loading.value = false
    }
  }

  async function refreshRun(runId = view.value?.run.id) {
    const run = view.value?.run
    if (!runId || disposed) return
    stopPolling()
    const current = generation
    const revision = ++viewRevision
    const scope = requestScope()
    const previousStatus = run?.status
    const previousLogs = view.value?.run.id === runId ? view.value.logs : []
    const lastLog = previousLogs.slice(-1)[0]?.id || 0
    readingRun.value = true
    runError.value = ''
    try {
      const data = await localBuildApi.view(runId, lastLog, scope.signal)
      if (disposed || current !== generation || revision !== viewRevision) return
      if (data.run.id !== runId || data.run.appId !== appId()) throw new Error(tr('构建任务与当前项目不匹配，请重新读取。'))
      const merged = [...previousLogs, ...(data.logs || [])]
      const unique = new Map(merged.map(line => [line.id, line]))
      view.value = { ...data, logs: [...unique.values()].sort((a, b) => a.id - b.id).slice(-3000),
        targets: data.targets || [], artifacts: data.artifacts || [] }
      recentRuns.value = [data.run, ...recentRuns.value.filter(item => item.id !== runId)]
      writeStorage('view', runId)
      if (run && isRunning(run) && !isRunning(data.run) && previousStatus !== data.run.status) void load()
    } catch (reason) {
      if (!disposed && current === generation && revision === viewRevision) {
        runError.value = scope.signal.aborted ? tr('读取超时，请重试。') : messageOf(reason)
      }
    } finally {
      scope.release()
      if (!disposed && current === generation && revision === viewRevision) {
        readingRun.value = false
        pollAgain()
      }
    }
  }

  async function showRun(run: LocalBuildRun) {
    if (busy.value || disposed) return
    stopPolling()
    viewRevision++
    cancelError.value = ''
    view.value = { run, targets: [], logs: [], artifacts: [], outputDirectory: '' }
    writeStorage('view', run.id)
    await refreshRun()
  }

  async function start(recover = false) {
    if (submitting.value || cancelling.value || disposed) return
    if (!recover && !canStart.value) return
    const request = recover ? pendingRequest.value : {
      requestId: crypto.randomUUID(), configFingerprint: preparation.value!.configFingerprint,
      targetIds: [...selectedIds.value],
    }
    if (!request) return
    const current = generation
    const id = appId()
    const scope = requestScope(60000)
    pendingRequest.value = request
    // The same idempotency key survives ambiguous network results and a closed panel.
    writeStorage('pending', JSON.stringify(request))
    submitting.value = true
    actionError.value = ''
    try {
      const run = await localBuildApi.create(id, request, scope.signal)
      if (disposed || current !== generation) return
      pendingRequest.value = null
      writeStorage('pending', '')
      recentRuns.value = [run, ...recentRuns.value.filter(item => item.id !== run.id)]
      submitting.value = false
      await showRun(run)
    } catch (reason) {
      if (disposed || current !== generation) return
      if (reason instanceof LocalBuildError && reason.status >= 400 && reason.status < 500) {
        pendingRequest.value = null
        writeStorage('pending', '')
        if (reason.status === 409) void load(true)
      }
      actionError.value = scope.signal.aborted ? tr('请求超时，请确认任务状态后继续。') : messageOf(reason)
    } finally {
      scope.release()
      if (!disposed && current === generation) submitting.value = false
    }
  }

  async function cancel() {
    if (!view.value || !active.value || busy.value) return
    const current = generation
    const id = view.value.run.id
    const scope = requestScope()
    stopPolling()
    cancelling.value = true
    cancelError.value = ''
    try {
      await localBuildApi.cancel(id, scope.signal)
      if (!disposed && current === generation && view.value?.run.id === id) await refreshRun()
    } catch (reason) {
      if (!disposed && current === generation) cancelError.value = scope.signal.aborted
        ? tr('取消请求超时，请重新读取任务状态。') : messageOf(reason)
    } finally {
      scope.release()
      if (!disposed && current === generation) cancelling.value = false
    }
  }

  function readTaskStatus() {
    // Only an explicit status retry dismisses a prior cancellation error.
    cancelError.value = ''
    return refreshRun()
  }

  function newBuild() {
    if (busy.value || active.value || pendingRequest.value) return
    stopPolling()
    viewRevision++
    view.value = null
    runError.value = ''
    actionError.value = ''
    cancelError.value = ''
    writeStorage('view', '')
    void load()
  }
  watch(appId, () => {
    resetRequests()
    preparation.value = null
    recentRuns.value = []
    selectedIds.value = []
    view.value = null
    pendingRequest.value = restorePending()
    submitting.value = false
    cancelling.value = false
    readingRun.value = false
    runError.value = ''
    actionError.value = ''
    cancelError.value = ''
    void load(true)
  }, { immediate: true, flush: 'sync' })
  onBeforeUnmount(() => { disposed = true; resetRequests() })
  return { preparation, recentRuns, selectedIds, view, pendingRequest, loading, submitting,
    cancelling, readingRun, error, runError, actionError, cancelError, active, existingActive, busy, canStart,
    load, start, cancel, showRun, refreshRun, readTaskStatus, newBuild }
}
