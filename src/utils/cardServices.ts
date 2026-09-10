import type { AppService, AppView } from '@/types'

export type CardService = AppService & { source: 'current' | 'history' | 'configured' }

export function cardServices(app: AppView): CardService[] {
  const rows: CardService[] = []
  const ports = new Set<number>()
  const active = ['starting', 'running', 'degraded', 'stopping'].includes(app.status)
  for (const [services, source] of [[app.services, active ? 'current' : 'history'], [app.knownServices, 'history']] as const) {
    for (const service of services || []) {
      if (ports.has(service.port)) continue
      ports.add(service.port)
      rows.push({ ...service, source })
    }
  }
  for (const port of app.portHints || []) {
    if (ports.has(port) || port < 1 || port > 65535) continue
    ports.add(port)
    rows.push({ id: `configured-${port}`, appId: app.id, appRunId: '', port, url: '', health: 'unknown',
      detectedAt: '', lastChecked: '', role: 'unknown', roleSource: 'auto', source: 'configured' })
  }
  const order = { frontend: 0, backend: 1, database: 2, unknown: 3 }
  return rows.sort((a, b) => (order[a.role || 'unknown'] - order[b.role || 'unknown']) || a.port - b.port)
}
