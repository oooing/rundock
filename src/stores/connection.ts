// 连接状态：sidecar 是否就绪 + WebSocket 是否连上。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { pingSidecar } from '@/api/base'
import { wsClient } from '@/api/ws'

export const useConnectionStore = defineStore('connection', () => {
  const sidecarReady = ref(false)
  const wsConnected = ref(false)
  const checking = ref(false)
  const error = ref('')

  let pollTimer: number | null = null

  async function check() {
    checking.value = true
    error.value = ''
    try {
      const ok = await pingSidecar(2000)
      sidecarReady.value = ok
      if (ok) {
        wsClient.connect()
      }
    } catch (e: any) {
      error.value = e?.message || String(e)
    } finally {
      checking.value = false
    }
  }

  function startPolling() {
    if (pollTimer != null) return
    // 先同步 ws 状态
    const syncWs = () => {
      wsConnected.value = wsClient.isConnected
    }
    wsClient.on(() => {
      /* 消息由 apps store 处理；这里只维持连通感 */
    })
    void check()
    pollTimer = window.setInterval(() => {
      if (!sidecarReady.value) void check()
      syncWs()
    }, 3000)
  }

  function stopPolling() {
    if (pollTimer != null) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  return { sidecarReady, wsConnected, checking, error, check, startPolling, stopPolling }
})
