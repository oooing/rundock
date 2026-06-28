// WebSocket 客户端：连接 sidecar /ws，自动重连，分发消息给订阅者。
import { getWsURL } from './base'
import type { WSMessage } from '@/types'

type Handler = (msg: WSMessage) => void

class WSClient {
  private ws: WebSocket | null = null
  private handlers = new Set<Handler>()
  private reconnectTimer: number | null = null
  private shouldReconnect = true
  private alive = false

  get isConnected() {
    return this.alive
  }

  connect() {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return
    }
    this.shouldReconnect = true
    this.open()
  }

  private open() {
    try {
      this.ws = new WebSocket(getWsURL())
    } catch {
      this.scheduleReconnect()
      return
    }
    this.ws.onopen = () => {
      this.alive = true
    }
    this.ws.onclose = () => {
      this.alive = false
      if (this.shouldReconnect) this.scheduleReconnect()
    }
    this.ws.onerror = () => {
      // 关闭会触发 onclose -> 重连
      this.ws?.close()
    }
    this.ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as WSMessage
        this.handlers.forEach((h) => h(msg))
      } catch {
        /* ignore malformed */
      }
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer != null) return
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null
      this.open()
    }, 2000)
  }

  disconnect() {
    this.shouldReconnect = false
    if (this.reconnectTimer != null) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.ws?.close()
    this.ws = null
    this.alive = false
  }

  on(handler: Handler): () => void {
    this.handlers.add(handler)
    return () => this.handlers.delete(handler)
  }
}

export const wsClient = new WSClient()
