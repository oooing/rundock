// Apps store：App 列表 + 启停重启操作 + 接收 WS 实时更新。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/http'
import { wsClient } from '@/api/ws'
import type { AppView, CreateAppBody, ImportCandidate, WSMessage } from '@/types'

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
    }
    const created = await api.createApp(body)
    apps.value.push(created)
    return created
  }

  async function start(id: string) {
    await api.start(id)
    patch(id, { status: 'starting' })
  }
  async function stop(id: string) {
    patch(id, { status: 'stopping' })
    await api.stop(id)
  }
  async function restart(id: string) {
    patch(id, { status: 'starting' })
    await api.restart(id)
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

  /** 手动设置服务角色（锁定为 manual）。后端会广播 app:services，这里乐观更新避免等待。 */
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
        if (a) patch(a.id, { status: (msg.status as AppView['status']) || a.status })
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
    load, importRaw, createFromCandidate, start, stop, restart, remove, rename, openURL, openDir, update,
    setServiceRole, reidentifyService,
    patch, bindWS, clearLiveLogs,
  }
})
