// Apps store：App 列表 + 启停重启操作 + 接收 WS 实时更新。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/http'
import { wsClient } from '@/api/ws'
import { pickNextCardColor } from '@/utils/cardColors'
import type {
  AppView,
  CreateAppBody,
  ImportCandidate,
  PendingOp,
  ScriptConfirmationResponse,
  WSMessage,
} from '@/types'

/**
 * 启动/重启结果。
 * - confirmation：脚本风险变化需确认时返回（后端 409），调用方据此弹 ConfirmCard。
 * - configUpdatedToast：后端自动同步了脚本派生字段，调用方据此提示"脚本配置已自动更新"。
 */
export interface StartResult {
  confirmation?: ScriptConfirmationResponse
  configUpdatedToast?: boolean
}

export const useAppsStore = defineStore('apps', () => {
  const apps = ref<AppView[]>([])
  const loading = ref(false)
  const error = ref('')

  // 实时日志缓冲（按 appId），日志抽屉订阅
  const liveLogs = ref<Record<string, import('@/types').LogEntry[]>>({})

  async function load() {
    loading.value = true
    error.value = ''
    try {
      apps.value = await api.listApps()
    } catch (e: any) {
      error.value = e?.message || String(e)
    } finally {
      loading.value = false
    }
  }

  /** 只读导入：返回候选配置（不创建 App） */
  async function importRaw(scriptPath: string): Promise<ImportCandidate> {
    return api.import(scriptPath)
  }

  async function createFromCandidate(c: ImportCandidate): Promise<AppView> {
    const body: CreateAppBody = {
      name: c.name,
      entryScript: c.entryScript,
      cwd: c.cwd,
      adapterType: c.adapterType,
      cmd: c.cmd,
      args: c.args,
      env: c.env,
      tags: [],
      portHints: c.portHints,
      healthUrl: '',
      scriptHash: c.scriptHash,
      cardColor: pickNextCardColor(apps.value.map((a) => a.cardColor)),
    }
    const created = await api.createApp(body)
    apps.value.push(created)
    return created
  }

  /**
   * 启动一个 app。可能返回 confirmation（脚本风险变化需确认）。
   * 调用方（App.vue）在 confirmation 时记下 pending op + 候选，由 ConfirmCard 二次确认。
   * confirmedScriptHash：用户确认风险后回带，让后端校验哈希是否仍匹配。
   */
  async function start(id: string, confirmedScriptHash?: string): Promise<StartResult> {
    try {
      const r = await api.start(id, confirmedScriptHash)
      if (!r.ok) {
        // 409：脚本风险变化需确认，不动本地状态
        return { confirmation: r.confirmation }
      }
      if (r.data.configUpdated && r.data.app) {
        patchFull(r.data.app)
      }
      patch(id, { status: 'starting' })
      return { configUpdatedToast: !!r.data.configUpdated }
    } catch (e: any) {
      error.value = e?.message || String(e)
      throw e
    }
  }
  async function stop(id: string) {
    const prev = apps.value.find(a => a.id === id)?.status
    patch(id, { status: 'stopping' })
    try {
      await api.stop(id)
      // 兜底：API 成功但 WS 未推送 stopped（应用已停止的幂等情况），直接收敛状态
      const cur = apps.value.find(a => a.id === id)?.status
      if (cur === 'stopping') {
        patch(id, { status: 'stopped' })
      }
    } catch (e: any) {
      if (prev) patch(id, { status: prev })
      error.value = e?.message || String(e)
      throw e
    }
  }
  /**
   * 重启一个 app。同 start，可能返回 confirmation。
   * confirmedScriptHash：用户确认风险后回带。
   */
  async function restart(id: string, confirmedScriptHash?: string): Promise<StartResult> {
    const prev = apps.value.find(a => a.id === id)?.status
    patch(id, { restarting: true })
    try {
      const r = await api.restart(id, confirmedScriptHash)
      if (!r.ok) {
        // 409：取消 restarting 标志，等用户确认后再发起
        patch(id, { ...(prev ? { status: prev } : {}), restarting: false })
        return { confirmation: r.confirmation }
      }
      if (r.data.configUpdated && r.data.app) {
        patchFull(r.data.app)
      }
      return { configUpdatedToast: !!r.data.configUpdated }
    } catch (e: any) {
      patch(id, { ...(prev ? { status: prev } : {}), restarting: false })
      error.value = e?.message || String(e)
      throw e
    }
  }

  /**
   * 用户在 ConfirmCard 上确认风险后，按记下的 pending op 重试原操作。
   *   - op='start' → apps.start(id, hash)
   *   - op='restart' → apps.restart(id, hash)
   * 返回值与 start/restart 一致（若期间脚本再次变化，会再带 confirmation）。
   */
  async function resumeAfterConfirm(id: string, op: PendingOp, confirmedScriptHash: string): Promise<StartResult> {
    if (op === 'restart') return restart(id, confirmedScriptHash)
    return start(id, confirmedScriptHash)
  }
  async function remove(id: string) {
    await api.deleteApp(id)
    apps.value = apps.value.filter((a) => a.id !== id)
  }
  async function rename(id: string, name: string) {
    await api.updateApp(id, { name })
    patch(id, { name })
  }
  async function openURL(id: string, url?: string) {
    await api.openURL(id, url)
  }
  async function openDir(id: string) {
    await api.openDir(id)
  }

  async function update(id: string, body: Record<string, unknown>) {
    const updated = await api.updateApp(id, body)
    patchFull(updated)
  }

  /** 设置卡片背景色（文字色由前端实时计算，不持久化）。 */
  async function setCardColor(id: string, cardColor: string) {
    const updated = await api.updateApp(id, { cardColor })
    patchFull(updated)
  }

  /** 手动设置服务角色（锁定为 manual）。后端会广播 app:services，这里在请求确认后立即本地更新，避免等待 WS 广播造成闪烁。 */
  async function setServiceRole(appId: string, serviceId: string, role: import('@/types').ServiceRole) {
    await api.setServiceRole(appId, serviceId, role)
    const a = apps.value.find((x) => x.id === appId)
    if (a && a.services) {
      a.services = a.services.map((s) =>
        s.id === serviceId ? { ...s, role, roleSource: 'manual' as const } : s
      )
    }
  }

  /** 强制重新识别某服务角色（重置为 auto）。后端重新探测后广播 app:services，本地不乐观更新。 */
  async function reidentifyService(appId: string, serviceId: string) {
    await api.reidentifyService(appId, serviceId)
  }

  function patch(id: string, p: Partial<AppView>) {
    const idx = apps.value.findIndex((a) => a.id === id)
    if (idx >= 0) apps.value[idx] = { ...apps.value[idx], ...p }
  }
  function patchFull(a: AppView) {
    const idx = apps.value.findIndex((x) => x.id === a.id)
    if (idx >= 0) apps.value[idx] = a
  }

  /** 接收 WS 消息，更新本地状态 */
  function handleWS(msg: WSMessage) {
    if (!msg.appId) return
    const a = apps.value.find((x) => x.id === msg.appId)
    if (!a && msg.type !== 'app:log') {
      // 未知 app 的非日志消息：可能是新增，刷新一次
      void load()
      return
    }
    switch (msg.type) {
      case 'app:status':
        if (a) {
          const status = (msg.status as AppView['status']) || a.status
          const restartFinished = ['running', 'degraded', 'failed'].includes(status)
          patch(a.id, {
            status,
            ...(a.restarting && restartFinished ? { restarting: false } : {}),
          })
        }
        break
      case 'app:url':
        if (a) patch(a.id, { lastUrl: msg.url || a.lastUrl })
        break
      case 'app:services':
        // 多服务状态更新：替换该 app 的 services 数组
        if (a && msg.services) {
          apps.value = apps.value.map((x) =>
            x.id === a.id ? { ...x, services: msg.services! } : x
          )
        }
        break
      case 'app:log':
        if (msg.log) {
          const arr = liveLogs.value[msg.appId] || []
          arr.push(msg.log)
          // 限制内存：每 app 最多保留 2000 条
          if (arr.length > 2000) arr.splice(0, arr.length - 2000)
          liveLogs.value = { ...liveLogs.value, [msg.appId]: arr }
        }
        break
      case 'app:event':
        // 事件可顺带作为提示日志
        break
    }
  }

  function clearLiveLogs(appId: string) {
    liveLogs.value = { ...liveLogs.value, [appId]: [] }
  }

  /** 订阅 WS（应在 App 启动时调用一次） */
  function bindWS() {
    wsClient.on(handleWS)
  }

  return {
    apps, loading, error, liveLogs,
    load, importRaw, createFromCandidate, start, stop, restart, resumeAfterConfirm, remove, rename, openURL, openDir, update, setCardColor,
    setServiceRole, reidentifyService,
    patch, bindWS, clearLiveLogs,
  }
})
