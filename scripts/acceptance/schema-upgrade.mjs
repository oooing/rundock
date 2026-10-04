// Real process/HTTP/SQLite acceptance. Failure inventory: docs/adr/2026-10-04-schema-upgrade-order.md.
// Requires Node >= 22.13 (node:sqlite) and a separately compiled sidecar; no live app data is used.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createHash, randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { fileURLToPath } from 'node:url'
import { makeFixture } from './local-build-fixture.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const executable = path.resolve(process.env.RUNDOCK_SIDECAR || path.join(root, 'sidecar/.tmp/schema-upgrade.exe'))
assert.ok(existsSync(executable), 'Compile the acceptance sidecar before running this script')
const evidence = path.join(root, 'outputs/acceptance', `schema-upgrade-${Date.now()}-${randomUUID().slice(0, 8)}`)
mkdirSync(evidence, { recursive: true })
const fixtureRoot = mkdtempSync(path.join(tmpdir(), 'rundock-schema-upgrade-'))
const project = makeFixture(fixtureRoot, 'project')
const report = { passed: false, boundary: 'isolated actual sidecar startup, HTTP, SQLite, Node build and artifact bytes; no public release or installed-version changes',
  executable: { path: executable, sha256: digest(readFileSync(executable)) }, fixtureRoot, checks: [], scenarios: [] }
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
function digest(bytes) { return createHash('sha256').update(bytes).digest('hex') }
function record(name) { report.checks.push(name) }
function database(data, readOnly = true) { return new DatabaseSync(path.join(data, 'launcher.db'), { readOnly }) }
function withDatabase(data, action, readOnly = true) {
  const db = database(data, readOnly)
  try { return action(db) } finally { db.close() }
}

function legacy(data) {
  return withDatabase(data, db => {
    db.exec(readFileSync(path.join(root, 'sidecar/internal/store/migrations/001_init.sql'), 'utf8'))
    db.exec(`CREATE TABLE release_profiles (
      app_id TEXT PRIMARY KEY, remote_name TEXT NOT NULL DEFAULT 'origin',
      version_strategy TEXT NOT NULL DEFAULT 'auto', pre_release_command TEXT NOT NULL DEFAULT '',
      updated_at TEXT NOT NULL DEFAULT (datetime('now'))
    ); CREATE TABLE release_runs (
      id TEXT PRIMARY KEY, app_id TEXT NOT NULL, repo_root TEXT NOT NULL, branch TEXT NOT NULL,
      remote_name TEXT NOT NULL, target_version TEXT NOT NULL, tag_name TEXT NOT NULL,
      status TEXT NOT NULL DEFAULT 'queued', stage TEXT NOT NULL DEFAULT 'preparing',
      commit_sha TEXT NOT NULL DEFAULT '', status_fingerprint TEXT NOT NULL DEFAULT '',
      error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
      created_at TEXT NOT NULL DEFAULT (datetime('now')), finished_at TEXT
    );`)
    db.prepare('INSERT INTO apps(id,name,entry_script,cwd) VALUES(?,?,?,?)')
      .run('legacy-app', 'Migration fixture', path.join(project.root, 'start.cmd'), project.root)
    db.prepare('INSERT INTO release_profiles(app_id) VALUES(?)').run('legacy-app')
    db.prepare(`INSERT INTO release_runs(id,app_id,repo_root,branch,remote_name,target_version,tag_name,status)
      VALUES(?,?,?,?,?,?,?,?)`).run('legacy-run', 'legacy-app', project.root, 'main', 'origin', '1.2.3', 'v1.2.3', 'succeeded')
    return { profile: { ...db.prepare('SELECT * FROM release_profiles').get() }, run: { ...db.prepare('SELECT * FROM release_runs').get() } }
  }, false)
}

async function launch(data, label) {
  let log = '', exitError
  const backend = spawn(executable, ['-addr', '127.0.0.1:0'], { cwd: evidence, windowsHide: true,
    env: { ...process.env, LAUNCHER_DATA_DIR: data, APPDATA: data, LAUNCHER_UI_ORIGIN: 'http://127.0.0.1:1421' },
    stdio: ['ignore', 'pipe', 'pipe'] })
  backend.stdout.on('data', bytes => { log += bytes })
  backend.stderr.on('data', bytes => { log += bytes })
  backend.on('error', error => { exitError = error })
  const exited = new Promise(resolve => backend.once('exit', resolve))
  let base
  async function stop() {
    if (base && backend.exitCode === null) {
      await fetch(base + '/api/desktop/shutdown', { method: 'POST', signal: AbortSignal.timeout(5000) }).catch(() => {})
      await Promise.race([exited, pause(7000)])
    }
    if (backend.exitCode === null && !exitError) { backend.kill(); await Promise.race([exited, pause(3000)]) }
    writeFileSync(path.join(evidence, `${label}.log`), log)
  }
  try {
    const end = Date.now() + 30000
    while (Date.now() < end) {
      if (exitError) throw exitError
      if (backend.exitCode !== null) throw new Error(`Sidecar exited (${backend.exitCode}): ${log}`)
      const portFile = path.join(data, 'sidecar.port')
      if (existsSync(portFile)) {
        const port = readFileSync(portFile, 'utf8').trim()
        assert.match(port, /^\d+$/)
        base = `http://127.0.0.1:${port}`
        try { if ((await (await fetch(base + '/api/health', { signal: AbortSignal.timeout(1000) })).json()).status === 'ok') break } catch {}
      }
      await pause(100)
    }
    assert.ok(base, 'Sidecar did not publish an isolated port')
    const request = async (route, method = 'GET', body) => {
      const res = await fetch(base + route, { method, headers: { 'Content-Type': 'application/json', Origin: 'http://127.0.0.1:1421' },
        body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(30000) })
      const value = await res.json()
      assert.ok(res.ok, `${method} ${route}: ${JSON.stringify(value)}`)
      return value
    }
    assert.equal((await request('/api/health')).status, 'ok')
    return { base, request, stop }
  } catch (error) { await stop(); throw error }
}

function verifySchema(data, before) {
  withDatabase(data, db => {
    const columns = db.prepare('PRAGMA table_info(release_runs)').all().map(row => row.name)
    for (const column of ['execution_plan_json', 'create_tag', 'selected_targets_json']) assert.ok(columns.includes(column))
    const index = db.prepare("SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_local_build_request'").get()
    assert.match(index?.sql || '', /UNIQUE INDEX/i)
    assert.match(index.sql, /json_extract\(execution_plan_json/)
    assert.match(index.sql, /build-only/)
    assert.equal(db.prepare('PRAGMA integrity_check').get().integrity_check, 'ok')
    if (before) {
      for (const [table, expected] of [['release_profiles', before.profile], ['release_runs', before.run]]) {
        const actual = db.prepare(`SELECT * FROM ${table}`).get()
        for (const [key, value] of Object.entries(expected)) assert.equal(actual[key], value, `${table}.${key} changed`)
      }
      const defaults = db.prepare("SELECT * FROM release_runs WHERE id='legacy-run'").get()
      assert.equal(defaults.execution_plan_json, '[]'); assert.equal(defaults.create_tag, 1)
      assert.equal(defaults.selected_targets_json, '[]')
    }
  })
}

async function completed(server, runId) {
  const end = Date.now() + 60000
  while (Date.now() < end) {
    const view = await server.request(`/api/local-builds/${runId}`)
    if (!['queued', 'running', 'pending'].includes(view.run.status)) {
      assert.equal(view.run.status, 'succeeded', JSON.stringify(view)); return view
    }
    await pause(100)
  }
  throw new Error('Local build did not finish')
}

const selected = process.argv.find(arg => arg.startsWith('--scenario='))?.split('=')[1]
assert.ok(!selected || ['fresh', 'legacy'].includes(selected), 'Scenario must be fresh or legacy')
try {
  for (const scenario of selected ? [selected] : ['legacy', 'fresh']) {
    const data = path.join(evidence, scenario, 'data')
    mkdirSync(data, { recursive: true })
    const before = scenario === 'legacy' ? legacy(data) : null
    let server
    try {
      server = await launch(data, `${scenario}-startup`)
      verifySchema(data, before)
      record(`${scenario}: actual startup, dependent index, integrity and preserved rows`)
      let app
      if (before) {
        app = (await server.request('/api/apps')).find(item => item.id === 'legacy-app')
        assert.equal(app.name, 'Migration fixture')
        const profile = await server.request('/api/apps/legacy-app/release-profile')
        assert.equal(profile.createTag, true); assert.equal(profile.versionMode, 'auto'); assert.equal(profile.buildMode, 'github')
        const view = await server.request('/api/releases/legacy-run')
        assert.equal(view.run.tagName, 'v1.2.3'); assert.equal(view.run.status, 'succeeded')
        record('legacy: project, safe defaults and successful release history readable through HTTP')
      } else {
        app = await server.request('/api/apps', 'POST', { name: 'Fresh fixture', entryScript: path.join(project.root, 'start.cmd'),
          cwd: project.root, adapterType: 'batch' })
      }
      const prep = (await server.request(`/api/apps/${app.id}/local-builds`)).preparation
      const body = { requestId: randomUUID(), configFingerprint: prep.configFingerprint, targetIds: ['good'] }
      const run = await server.request(`/api/apps/${app.id}/local-builds`, 'POST', body)
      const view = await completed(server, run.id)
      const repeated = await server.request(`/api/apps/${app.id}/local-builds`, 'POST', body)
      assert.equal(repeated.id, run.id)
      const artifact = view.artifacts.find(item => item.name.endsWith('.cjs.gz'))
      assert.ok(artifact?.available)
      const response = await fetch(`${server.base}/api/local-builds/${run.id}/artifacts/${artifact.id}`, {
        headers: { Origin: 'http://127.0.0.1:1421' }, signal: AbortSignal.timeout(5000) })
      assert.equal(response.status, 200)
      assert.equal(digest(Buffer.from(await response.arrayBuffer())), artifact.sha256)
      record(`${scenario}: real local build, HTTP duplicate request and exact artifact download`)
      await server.stop(); server = null
      withDatabase(data, db => {
        const current = db.prepare('SELECT * FROM release_runs WHERE id=?').get(run.id)
        const columns = Object.keys(current).filter(column => column !== 'id')
        const insert = db.prepare(`INSERT INTO release_runs(id,${columns.join(',')}) VALUES(${columns.map(() => '?').concat('?').join(',')})`)
        assert.throws(() => insert.run('duplicate-fixture-request', ...columns.map(column => current[column])), /UNIQUE constraint failed/)
        assert.equal(db.prepare('SELECT COUNT(*) AS n FROM release_runs WHERE id=?').get('duplicate-fixture-request').n, 0)
      }, false)
      record(`${scenario}: database independently enforces the persistent request uniqueness constraint`)
      server = await launch(data, `${scenario}-restart`)
      verifySchema(data)
      const afterRestart = await server.request(`/api/apps/${app.id}/local-builds`, 'POST', body)
      assert.equal(afterRestart.id, run.id)
      assert.equal((await completed(server, run.id)).artifacts[0].sha256, view.artifacts[0].sha256)
      record(`${scenario}: already-indexed database reopens and replays the same request without rebuilding`)
      report.scenarios.push({ scenario, data, runId: run.id, artifactSHA256: artifact.sha256 })
    } finally { if (server) await server.stop() }
  }
  report.passed = true
} catch (error) { report.error = error.stack || String(error); process.exitCode = 1 }
finally {
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  console.log(JSON.stringify({ passed: report.passed, checks: report.checks.length, report: path.join(evidence, 'report.json'), error: report.error }))
}
