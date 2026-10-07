import { computed, onBeforeUnmount, ref, watch, type ComputedRef } from 'vue'
import { api } from '@/api/http'
import { useAppsStore } from '@/stores/apps'
import { tr } from '@/i18n'
import type { AppView, StartupIssue } from '@/types'

/** Card diagnostics are read-only; closing programs requires the separate confirmation dialog. */
export function useAppStartup(app: ComputedRef<AppView>) {
  const appsStore = useAppsStore()
  const runtimeLocked = computed(() => !!app.value.runtimeCheck && app.value.runtimeCheck.state !== 'clear')
  const startupIssue = ref<StartupIssue | null>(null)
  const checkingIssue = ref(false)
  const resolvingPorts = ref(false)
  let issueRequest = 0
  let disposed = false
  const operationBusy = computed(() => !!appsStore.operationBusy[app.value.id] || resolvingPorts.value)
  const portIssue = computed(() => startupIssue.value?.code === 'port_in_use')
  const statusLabel = computed(() => {
    if (app.value.runtimeCheck?.state === 'conflict') return tr('端口被占用')
    if (app.value.runtimeCheck?.state === 'reserved') return tr('系统保留端口')
    if (app.value.restarting) return tr('重启中')
    const labels: Record<string, string> = {
      starting: tr('启动中'), running: tr('运行中'), degraded: tr('降级'), stopping: tr('停止中'),
      stopped: tr('已停止'), failed: tr('失败'), checking: tr('正在检查'), unknown: tr('状态待确认'),
    }
    return labels[app.value.status] || app.value.status
  })

  async function checkStartupIssue() {
    const request = ++issueRequest
    const id = app.value.id
    if (app.value.status !== 'failed') {
      startupIssue.value = null
      checkingIssue.value = false
      return
    }
    checkingIssue.value = true
    try {
      const result = await api.startupIssue(id)
      if (!disposed && request === issueRequest && app.value.id === id) startupIssue.value = result
    } catch {
      if (!disposed && request === issueRequest) startupIssue.value = null
    } finally {
      if (!disposed && request === issueRequest) checkingIssue.value = false
    }
  }
  watch(() => [app.value.id, app.value.status, app.value.runId], checkStartupIssue, { immediate: true })
  watch(() => app.value.id, () => {
    resolvingPorts.value = false
  })
  onBeforeUnmount(() => { disposed = true; issueRequest++ })
  return { runtimeLocked, startupIssue, checkingIssue, resolvingPorts, operationBusy, portIssue, statusLabel }
}
