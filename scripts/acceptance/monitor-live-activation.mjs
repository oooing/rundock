// Read-only observation of the explicitly authorized installed update.
import fs from 'node:fs'
import path from 'node:path'
const dir = path.resolve('outputs/acceptance/activation-live-20261018')
fs.mkdirSync(dir, { recursive: true })
const baseline = JSON.parse(fs.readFileSync(path.join(dir, 'before-17655.json'), 'utf8'))
const stop = path.join(dir, 'stop-monitor')
const out = path.join(dir, `samples-${Date.now()}.jsonl`)
const summary = { startedAt: new Date().toISOString(), samples: 0, devErrors: [], out }
async function get(url, json = true) {
  try {
    const r = await fetch(url, { signal: AbortSignal.timeout(4000) })
    return json ? { status: r.status, body: await r.json() } : { status: r.status }
  } catch (e) { return { error: String(e) } }
}
const end = Date.now() + 45 * 60 * 1000
while (!fs.existsSync(stop) && Date.now() < end) {
  const [dev, web, installed, app, front, back, metro] = await Promise.all([
    get('http://127.0.0.1:17655/api/health'), get('http://127.0.0.1:17656/', false),
    get('http://127.0.0.1:17654/api/health'), get('http://127.0.0.1:17654/api/apps/f93f04cfd0e5039e'),
    get('http://127.0.0.1:9100/', false), get('http://127.0.0.1:18009/', false), get('http://127.0.0.1:8081/status', false)
  ])
  const sample = { at: new Date().toISOString(), dev, web, installed, localPlay: app.body ? {
    status: app.body.status, pid: app.body.pid, runId: app.body.runId,
    services: app.body.services?.map(s => ({ port: s.port, health: s.health }))
  } : app, front, back, metro }
  if (dev.body?.instanceId !== baseline.health.instanceId || web.status !== 200) summary.devErrors.push(sample.at)
  fs.appendFileSync(out, JSON.stringify(sample) + '\n')
  summary.samples++
  fs.writeFileSync(path.join(dir, 'monitor-summary.json'), JSON.stringify(summary, null, 2))
  await new Promise(r => setTimeout(r, 1000))
}
summary.finishedAt = new Date().toISOString()
fs.writeFileSync(path.join(dir, 'monitor-summary.json'), JSON.stringify(summary, null, 2))
console.log(JSON.stringify(summary))
