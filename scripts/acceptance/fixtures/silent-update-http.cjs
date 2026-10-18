// Acceptance-only real HTTP process. The control file is deliberately outside
// the launch configuration so fault injection does not change its fingerprint.
const fs = require('node:fs')
const http = require('node:http')
const path = require('node:path')
const { randomUUID } = require('node:crypto')
const [port, token] = process.argv.slice(2)
const directory = process.cwd()
const identity = { token, pid: process.pid, instance: randomUUID() }
const events = path.join(directory, 'events.jsonl')
const record = (event, details = {}) => fs.appendFileSync(events,
  JSON.stringify({ event, ...identity, ...details, at: new Date().toISOString() }) + '\n')
let exiting = false
function exit() {
  if (exiting) return
  exiting = true
  record('exit')
  server.close(() => process.exit(0))
  server.closeIdleConnections()
}
const server = http.createServer((req, res) => {
  if (req.method === 'POST' && req.url === '/__acceptance/exit' &&
      req.headers['x-acceptance-token'] === token) {
    res.end('stopping')
    setImmediate(exit)
    return
  }
  res.setHeader('content-type', 'application/json')
  res.end(JSON.stringify(identity))
}).listen(Number(port), '127.0.0.1', () => {
  record('start')
  console.log(`http://127.0.0.1:${port}/`)
})
for (const signal of ['SIGINT', 'SIGBREAK', 'SIGTERM']) {
  process.on(signal, () => {
    const refused = fs.existsSync(path.join(directory, 'refuse-stop'))
    record('signal', { signal, refused })
    if (!refused) exit()
  })
}
// An interrupted runner must not leave an indefinite orphan fixture.
setTimeout(exit, 10 * 60_000).unref()
