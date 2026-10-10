import type { AppService, AppView } from '@/types'

export type CardService = AppService & { source: 'current' | 'history' | 'configured' }

const activeStates = ['starting', 'running', 'degraded', 'stopping']
const validPort = (port: number) => Number.isInteger(port) && port >= 1 && port <= 65535
const roleOrder = { frontend: 0, backend: 1, database: 2, unknown: 3 }
const sortServices = (rows: CardService[]) => rows.sort((a, b) =>
  (Number(a.statusScope === 'auxiliary') - Number(b.statusScope === 'auxiliary')) || (roleOrder[a.role || 'unknown'] - roleOrder[b.role || 'unknown']) || a.port - b.port)

// The card count describes discovered services, never configuration candidates.
export function cardServices(app: AppView): CardService[] {
  const rows: CardService[] = []
  const ports = new Set<number>()
  const active = activeStates.includes(app.status)
  const sources = active ? [app.services] : [app.services, app.knownServices]
  for (const services of sources) {
    for (const service of services || []) {
      if (!validPort(service.port) || ports.has(service.port)) continue
      ports.add(service.port)
      rows.push({ ...service, source: active ? 'current' : 'history' })
    }
  }
  return sortServices(rows)
}

// Keep omitted records available for diagnosis without implying they are live.
export function cardServiceDetails(app: AppView): { history: CardService[]; configured: CardService[] } {
  const ports = new Set(cardServices(app).map(service => service.port))
  const history: CardService[] = []
  const configured: CardService[] = []
  for (const service of app.knownServices || []) {
    if (!validPort(service.port) || ports.has(service.port)) continue
    ports.add(service.port)
    history.push({ ...service, source: 'history' })
  }
  for (const port of app.portHints || []) {
    if (!validPort(port) || ports.has(port)) continue
    ports.add(port)
    configured.push({ id: `configured-${port}`, appId: app.id, appRunId: '', port, url: '', health: 'unknown',
      detectedAt: '', lastChecked: '', role: 'unknown', roleSource: 'auto', source: 'configured' })
  }
  return { history: sortServices(history), configured: sortServices(configured) }
}
