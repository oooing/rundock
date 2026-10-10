import { tr } from '@/i18n'
import type { AppService } from '@/types'

export function serviceHealthReason(service: AppService): string {
  const reason = service.healthReason || ''
  const status = /^http_status:(\d{3})$/.exec(reason)
  if (status) return tr('健康检查返回 HTTP {0}', [status[1]])
  const labels: Record<string, string> = {
    timeout: tr('健康检查超时'),
    connection_refused: tr('连接被拒绝，服务可能已停止'),
    connection_failed: tr('无法连接服务'),
    invalid_url: tr('健康检查地址无效'),
  }
  return labels[reason] || tr('健康检查未通过')
}
