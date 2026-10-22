import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { setImmediate } from 'node:timers/promises'
import test from 'node:test'
import ts from 'typescript'
import * as vue from 'vue'
import { compileScript, parse } from '@vue/compiler-sfc'
import { renderToString } from '@vue/server-renderer'

// Execute the production state machine. Only native IPC and timer scheduling are replaced.
const source = readFileSync(new URL('../../src/stores/appUpdate.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
function store(invoke) {
  const exports = {}, timers = new Map()
  let nextTimer = 0
  class Channel { onmessage = () => {} }
  const require = id => {
    if (id === 'vue') return vue
    assert.equal(id, '@tauri-apps/api/core')
    return { invoke, Channel }
  }
  new Function('exports', 'require', 'setTimeout', 'clearTimeout', compiled)(exports, require,
    (callback, delay) => { assert.equal(delay, 1000); const id = nextTimer++; timers.set(id, callback); return id },
    id => timers.delete(id))
  return Object.assign(exports, {
    timers,
    async tick() {
      assert.equal(timers.size, 1, 'one status poll should be scheduled')
      const [id, callback] = timers.entries().next().value
      timers.delete(id)
      callback()
      await setImmediate()
    },
  })
}
const info = { version: '2.0.44', notes: 'Release notes', url: '', size: 100 }
const project = { id: 'a', name: 'Project A', runId: 'run-1', configHash: 'hash-1', temporaryPort: 3001 }
const report = (phase, extra = {}) => ({ version: info.version, phase, projects: [], remaining: [], error: '', failures: [], ...extra })
const commands = calls => calls.map(c => c.cmd)
function native(calls, overrides = {}) {
  return async (cmd, args) => {
    calls.push({ cmd, args })
    if (cmd in overrides) return overrides[cmd](args)
    if (cmd === 'app_update_status') return null
    if (cmd === 'check_app_update') return { ...info }
    if (cmd === 'update_install_plan') return { fingerprint: 'snapshot-1', projects: [project] }
    assert.ok(['download_app_update', 'stage_app_update', 'install_app_update', 'cancel_scheduled_update', 'retry_update_restore'].includes(cmd), cmd)
  }
}
function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function ready(s, phase = 'ready') {
  s.appUpdate.phase = phase
  s.appUpdate.info = { ...info, installedLocally: phase === 'installed' }
}

const dialogSource = readFileSync(new URL('../../src/components/AppUpdateDialog.vue', import.meta.url), 'utf8')
const dialogScript = compileScript(parse(dialogSource).descriptor, { id: 'app-update-test', inlineTemplate: true }).content
const dialogCompiled = ts.transpileModule(dialogScript, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
async function renderDialog(s) {
  const exports = {}
  new Function('exports', 'require', dialogCompiled)(exports, id => {
    if (id === 'vue') return vue
    if (id === '@/i18n') return { tr: key => key }
    if (id === '@/stores/appUpdate') return s
    assert.equal(id, '@/tauri/window')
    return { getAppVersion: async () => '2.0.43', openProjectReleases: async () => {} }
  })
  return renderToString(vue.createSSRApp(exports.default))
}

test('an older completed attempt shows the running version and current recovery status, never a downgrade arrow', async () => {
  const s = store(native([], { app_update_status: () => report('finished', {
    version: '2.0.46', currentVersion: '2.0.47', backendVersion: '2.0.47', versionActive: true,
    remaining: [project], failures: ['Project A：项目已停止，可点击“恢复项目”重新启动。'],
  }) }))
  await s.refreshUpdateStatus()
  const html = await renderDialog(s)
  assert.match(html, /项目待恢复/)
  assert.match(html, /当前版本.*v2\.0\.47/)
  assert.doesNotMatch(html, /→|更新未完成|v2\.0\.46/)
  assert.match(html, /恢复项目/)
})

test('a newer window with an older backend offers continued activation instead of claiming completion', async () => {
  let value = report('finished', { version: '2.0.46', currentVersion: '2.0.48',
    backendVersion: '2.0.47', versionActive: true })
  const calls = [], s = store(native(calls, {
    app_update_status: () => value,
    cancel_scheduled_update: () => { value = null },
    check_app_update: () => ({ ...info, version: '2.0.48', installedLocally: true }),
  }))
  await s.refreshUpdateStatus()
  const html = await renderDialog(s)
  assert.match(html, /后台待更新/)
  assert.match(html, /继续更新/)
  assert.doesNotMatch(html, /更新完成|v2\.0\.46/)
  await s.recheckAppUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.info.version, '2.0.48')
  assert.ok(!commands(calls).some(c => ['install_app_update', 'retry_update_restore'].includes(c)))
})

test('finished results refresh on open and poll only while visible; archived results clear cached errors', async () => {
  let value = report('finished', { remaining: [project], failures: ['old warning'] })
  const s = store(native([], { app_update_status: () => value }))
  await s.refreshUpdateStatus({ background: true })
  assert.equal(s.timers.size, 0)
  s.openAppUpdate()
  await setImmediate()
  assert.equal(s.timers.size, 1)
  value = null
  await s.tick()
  assert.equal(s.appUpdate.phase, 'current')
  assert.equal(s.appUpdate.report, null)
  assert.equal(s.appUpdate.info, null)
  assert.equal(s.appUpdate.error, '')
  assert.equal(s.timers.size, 0)
})

test('unavailable live status cannot display stale project failures or offer recovery', async () => {
  const s = store(native([], { app_update_status: () => report('finished', {
    remaining: [project], failures: ['stale degraded warning'], statusError: '后台暂不可用',
  }) }))
  await s.refreshUpdateStatus()
  const html = await renderDialog(s)
  assert.match(html, /状态待确认/)
  assert.match(html, /后台暂不可用/)
  assert.doesNotMatch(html, /stale degraded warning/)
  assert.match(html, /disabled[^>]*>恢复项目/)
  s.appUpdate.report.remaining = []
  assert.doesNotMatch(await renderDialog(s), /项目已恢复，可以继续使用/)
})

test('keeping current project state explicitly acknowledges recovery without starting or stopping', async () => {
  const calls = [], s = store(native(calls))
  ready(s, 'finished')
  s.appUpdate.report = report('finished', { remaining: [project] })
  await s.cancelScheduledUpdate(true)
  assert.deepEqual(calls, [{ cmd: 'cancel_scheduled_update', args: { keepCurrentState: true } }])
  assert.equal(s.appUpdate.report, null)
  assert.equal(s.appUpdate.phase, 'idle')
})

test('startup checks, downloads and silently installs once without opening a dialog or stopping projects', async () => {
  const calls = [], s = store(native(calls))
  await Promise.all([s.prepareStartupUpdate(), s.prepareStartupUpdate()])
  await s.prepareStartupUpdate()
  assert.deepEqual(commands(calls), ['app_update_status', 'check_app_update', 'download_app_update', 'stage_app_update'])
  assert.deepEqual(calls.at(-1), { cmd: 'stage_app_update', args: undefined })
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.info.installedLocally, true)
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.equal(s.timers.size, 0)
  await s.installAppUpdate()
  await s.stageAppUpdate()
  assert.equal(calls.length, 4)
})

test('startup does not take over an already active user flow', async () => {
  const calls = [], s = store(native(calls))
  ready(s)
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'ready')
  assert.deepEqual(calls, [])
})

test('manual check opens the dialog and download becomes ready for silent installation', async () => {
  const calls = [], download = deferred()
  const s = store(native(calls, { download_app_update: ({ onProgress }) => {
    onProgress.onmessage({ downloaded: 60, total: 100 })
    return download.promise
  } }))
  await s.checkAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, true)
  const pending = s.downloadAppUpdate()
  assert.equal(s.appUpdate.phase, 'downloading')
  assert.equal(s.appUpdate.downloaded, 60)
  await s.downloadAppUpdate()
  await s.checkAppUpdate()
  download.resolve()
  await pending
  assert.equal(s.appUpdate.phase, 'ready')
  assert.deepEqual(commands(calls), ['app_update_status', 'check_app_update', 'download_app_update'])
  await s.stageAppUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.dialogOpen, true)
  s.dismissAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.equal(s.appUpdate.info.installedLocally, true)
  assert.equal(calls.length, 4, 'Got it only closes the dialog')
})

test('staging is dismissible and excludes duplicate installs, plans, downloads and cancellation', async () => {
  const calls = [], install = deferred(), s = store(native(calls, { stage_app_update: () => install.promise }))
  ready(s); s.openAppUpdate()
  const pending = s.stageAppUpdate()
  assert.equal(s.appUpdate.phase, 'staging')
  s.dismissAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, false)
  await Promise.all([s.stageAppUpdate(), s.scheduleAppUpdate(), s.planAppUpdate(), s.installAppUpdate(), s.downloadAppUpdate(), s.checkAppUpdate(), s.cancelScheduledUpdate()])
  assert.deepEqual(commands(calls), ['stage_app_update'])
  install.resolve()
  await pending
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.equal(s.timers.size, 0)
})

test('a confirmed stage failure stays visible, has no automatic loop and can be explicitly retried', async () => {
  const calls = []
  let fail = true
  const s = store(native(calls, { stage_app_update: () => { if (fail) throw Error('installer refused') } }))
  await s.prepareStartupUpdate()
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'ready')
  assert.match(s.appUpdate.error, /installer refused/)
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.equal(s.timers.size, 0)
  assert.equal(commands(calls).filter(c => c === 'stage_app_update').length, 1)
  s.openAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, true)
  fail = false
  await s.stageAppUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.error, '')
})

test('a staging timeout keeps polling through status errors, never submits another installer or reopens the dialog', async () => {
  const calls = []
  let status = report('staging', { error: 'installer still running' }), unavailable = false
  const s = store(native(calls, {
    stage_app_update: () => { throw Error('timeout') },
    app_update_status: () => { if (unavailable) throw Error('status unavailable'); return status },
  }))
  ready(s); s.openAppUpdate()
  await s.stageAppUpdate()
  assert.equal(s.appUpdate.phase, 'staging')
  assert.equal(s.appUpdate.error, 'installer still running')
  s.dismissAppUpdate()
  await s.stageAppUpdate()
  await s.planAppUpdate()
  unavailable = true
  await s.tick()
  assert.match(s.appUpdate.error, /status unavailable/)
  assert.equal(s.timers.size, 1)
  unavailable = false
  status = report('staged')
  await s.tick()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.info.installedLocally, true)
  assert.equal(s.appUpdate.info.notes, info.notes)
  assert.equal(s.appUpdate.error, '')
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.equal(s.timers.size, 0)
  assert.equal(commands(calls).filter(c => c === 'stage_app_update').length, 1)
})

test('an ambiguous IPC failure preserves staging until status can confirm the outcome', async () => {
  const calls = []
  let unavailable = true
  const s = store(native(calls, {
    stage_app_update: () => { throw Error('IPC timeout') },
    app_update_status: () => { if (unavailable) throw Error('disconnected'); return report('staged') },
  }))
  ready(s)
  await s.stageAppUpdate()
  assert.equal(s.appUpdate.phase, 'staging')
  assert.match(s.appUpdate.error, /IPC timeout/)
  await s.stageAppUpdate()
  unavailable = false
  await s.tick()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(commands(calls).filter(c => c === 'stage_app_update').length, 1)
})

test('stage failure with a staging report retains the IPC error when the report has no error', async () => {
  const calls = [], s = store(native(calls, {
    stage_app_update: () => { throw Error('timeout') },
    app_update_status: () => report('staging'),
  }))
  ready(s)
  await s.stageAppUpdate()
  await s.tick()
  assert.equal(s.appUpdate.phase, 'staging')
  assert.match(s.appUpdate.error, /timeout/)
  await s.stageAppUpdate()
  assert.equal(commands(calls).filter(c => c === 'stage_app_update').length, 1)
})

for (const rejects of [false, true]) test(`late status ${rejects ? 'failure' : 'response'} cannot undo successful installation`, async () => {
  const calls = [], install = deferred(), status = deferred()
  const s = store(native(calls, { stage_app_update: () => install.promise, app_update_status: () => status.promise }))
  ready(s)
  const pending = s.stageAppUpdate()
  await s.tick()
  const sharedRead = s.refreshUpdateStatus()
  assert.equal(commands(calls).filter(c => c === 'app_update_status').length, 1)
  install.resolve()
  await pending
  if (rejects) status.reject(Error('stale failure'))
  else status.resolve(report('staging'))
  await sharedRead
  await setImmediate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.info.installedLocally, true)
  assert.equal(s.appUpdate.error, '')
  assert.equal(s.timers.size, 0)
})

for (const phase of ['staging', 'staged', 'scheduled', 'restoring', 'finished']) test(`startup reconciles persisted ${phase} quietly without downloading or installing again`, async () => {
  const calls = [], s = store(native(calls, { app_update_status: () => report(phase) }))
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, phase === 'staged' ? 'installed' : phase)
  assert.equal(s.appUpdate.info.installedLocally, phase === 'staged')
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.deepEqual(commands(calls), ['app_update_status'])
  assert.equal(s.timers.size, ['staging', 'restoring'].includes(phase) ? 1 : 0)
})

test('an old scheduled record can be silently installed without rebuilding a stop plan', async () => {
  const calls = [], s = store(native(calls, { app_update_status: () => report('scheduled', { installer: { path: 'update.exe' } }) }))
  await s.prepareStartupUpdate()
  s.openAppUpdate()
  await s.planAppUpdate()
  assert.equal(s.appUpdate.phase, 'scheduled')
  assert.deepEqual(commands(calls), ['app_update_status'])
  await s.stageAppUpdate()
  assert.deepEqual(commands(calls), ['app_update_status', 'stage_app_update'])
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.info.installedLocally, true)
})

test('a failed old schedule retains its error and is retryable', async () => {
  const calls = [], s = store(native(calls, {
    stage_app_update: () => { throw Error('permission denied') },
    app_update_status: () => report('scheduled'),
  }))
  ready(s, 'scheduled')
  await s.stageAppUpdate()
  assert.equal(s.appUpdate.phase, 'scheduled')
  assert.match(s.appUpdate.error, /permission denied/)
  assert.equal(s.timers.size, 0)
})

test('old schedules with no installer represent an already installed version', async () => {
  const calls = [], s = store(native(calls, { app_update_status: () => report('scheduled', { installer: null }) }))
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.info.installedLocally, true)
  await s.cancelScheduledUpdate()
  await s.stageAppUpdate()
  assert.deepEqual(commands(calls), ['app_update_status'])
  await s.planAppUpdate()
  assert.equal(s.appUpdate.phase, 'confirming')
  assert.deepEqual(commands(calls), ['app_update_status', 'update_install_plan'])
})

test('an old schedule with an installer must stage even when cached info says installed', async () => {
  const calls = [], s = store(native(calls, { app_update_status: () => report('scheduled', { installer: { path: 'update.exe' } }) }))
  ready(s, 'installed')
  await s.refreshUpdateStatus()
  assert.equal(s.appUpdate.phase, 'scheduled')
  assert.equal(s.appUpdate.info.installedLocally, false)
  await s.planAppUpdate()
  assert.deepEqual(commands(calls), ['app_update_status'])
  await s.stageAppUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.info.installedLocally, true)
})

test('planning rejects ready and scheduled phases even if the local flag is set', async () => {
  const calls = [], s = store(native(calls))
  for (const phase of ['ready', 'scheduled']) {
    for (const installedLocally of [false, true]) {
      ready(s, phase); s.appUpdate.info.installedLocally = installedLocally
      await s.planAppUpdate()
      assert.equal(s.appUpdate.phase, phase)
      assert.equal(s.appUpdate.plan, null)
    }
  }
  assert.deepEqual(calls, [])
})

test('planning and activation require explicit installedLocally evidence, including stale confirmations', async () => {
  const calls = [], s = store(native(calls))
  for (const phase of ['installed', 'confirming']) {
    for (const installedLocally of [false, undefined]) {
      ready(s, phase); s.appUpdate.info.installedLocally = installedLocally
      s.appUpdate.plan = { fingerprint: 'stale', projects: [project] }
      await s.planAppUpdate()
      await s.installAppUpdate()
      assert.equal(s.appUpdate.phase, phase)
    }
  }
  assert.deepEqual(calls, [])
})

for (const phase of ['ready', 'scheduled', 'installed']) test(`activation from ${phase} requires installed files, a fresh list and exact confirmation fingerprint`, async () => {
  const calls = [], s = store(native(calls))
  ready(s, phase); s.openAppUpdate()
  await s.installAppUpdate()
  assert.deepEqual(calls, [])
  const staged = phase === 'installed' ? [] : ['stage_app_update']
  if (phase !== 'installed') {
    await s.planAppUpdate()
    assert.deepEqual(calls, [])
    assert.equal(s.appUpdate.phase, phase)
    await s.stageAppUpdate()
    assert.equal(s.appUpdate.phase, 'installed')
    assert.equal(s.appUpdate.info.installedLocally, true)
  }
  await s.planAppUpdate()
  assert.equal(s.appUpdate.phase, 'confirming')
  assert.deepEqual(s.appUpdate.plan.projects, [project])
  assert.deepEqual(commands(calls), [...staged, 'update_install_plan'])
  await s.installAppUpdate()
  assert.equal(s.appUpdate.phase, 'installing')
  assert.deepEqual(calls.at(-1), { cmd: 'install_app_update', args: { fingerprint: 'snapshot-1' } })
  await s.installAppUpdate()
  s.dismissAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, true)
  assert.equal(calls.length, staged.length + 2)
})

for (const phase of ['ready', 'scheduled', 'installed']) test(`failed confirmation after ${phase} invalidates the plan and preserves installed state`, async () => {
  const calls = [], s = store(native(calls, { install_app_update: () => { throw Error('Project list changed') } }))
  ready(s, phase)
  if (phase !== 'installed') await s.stageAppUpdate()
  await s.planAppUpdate()
  await s.installAppUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.plan, null)
  assert.match(s.appUpdate.error, /Project list changed/)
  assert.equal(s.appUpdate.info.installedLocally, true)
  await s.installAppUpdate()
  assert.equal(commands(calls).filter(c => c === 'install_app_update').length, 1)
})

test('an installed confirmation can refresh its project list before activation', async () => {
  const calls = [], s = store(native(calls, { update_install_plan: () => ({ fingerprint: `snapshot-${calls.length}`, projects: [project] }) }))
  ready(s, 'installed')
  await s.planAppUpdate()
  await s.planAppUpdate()
  assert.equal(s.appUpdate.phase, 'confirming')
  assert.equal(s.appUpdate.plan.fingerprint, 'snapshot-2')
  await s.installAppUpdate()
  assert.deepEqual(calls.at(-1), { cmd: 'install_app_update', args: { fingerprint: 'snapshot-2' } })
})

test('planning is deduplicated and a failed installed-version plan never becomes ready to reinstall', async () => {
  const calls = [], plan = deferred(), s = store(native(calls, { update_install_plan: () => plan.promise }))
  ready(s, 'installed'); s.openAppUpdate()
  const pending = s.planAppUpdate()
  await s.planAppUpdate()
  s.dismissAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, true)
  plan.reject(Error('cannot read projects'))
  await pending
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.plan, null)
  assert.match(s.appUpdate.error, /cannot read projects/)
  assert.equal(calls.length, 1)
})

test('back and dismiss discard a confirmation without installing or changing the default choice', async () => {
  const calls = [], s = store(native(calls))
  ready(s, 'installed'); s.openAppUpdate()
  await s.planAppUpdate()
  s.cancelAppUpdatePlan()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.plan, null)
  assert.equal(s.appUpdate.dialogOpen, true)
  await s.planAppUpdate()
  s.dismissAppUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.dialogOpen, false)
  await s.installAppUpdate()
  assert.deepEqual(commands(calls), ['update_install_plan', 'update_install_plan'])
})

test('external installation stays quiet and only activates after stop-plan confirmation', async () => {
  const calls = [], s = store(native(calls, { check_app_update: () => ({ ...info, installedLocally: true }) }))
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.dialogOpen, false)
  await s.stageAppUpdate()
  await s.installAppUpdate()
  assert.deepEqual(commands(calls), ['app_update_status', 'check_app_update'])
  await s.planAppUpdate()
  assert.equal(s.appUpdate.phase, 'confirming')
})

test('download failure stays quiet and retryable without a startup loop', async () => {
  const calls = []
  let fail = true
  const s = store(native(calls, { download_app_update: () => { if (fail) throw Error('offline') } }))
  await s.prepareStartupUpdate()
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'available')
  assert.match(s.appUpdate.error, /offline/)
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.equal(commands(calls).filter(c => c === 'download_app_update').length, 1)
  assert.ok(!commands(calls).includes('stage_app_update'))
  fail = false
  await s.downloadAppUpdate()
  assert.equal(s.appUpdate.phase, 'ready')
  assert.equal(s.appUpdate.error, '')
})

for (const command of ['app_update_status', 'check_app_update']) test(`${command} failure is visible without version info and an explicit check can recover`, async () => {
  const calls = []
  let fail = true
  const s = store(native(calls, { [command]: () => {
    if (fail) throw Error('offline')
    return command === 'app_update_status' ? null : { ...info }
  } }))
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'idle')
  assert.equal(s.appUpdate.info, null)
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.match(s.appUpdate.error, /offline/)
  s.openAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, true)
  fail = false
  await s.checkAppUpdate()
  assert.equal(s.appUpdate.phase, 'available')
  assert.equal(s.appUpdate.error, '')
})

test('retrying an unavailable startup status first detects staging instead of starting another download', async () => {
  const calls = []
  let fail = true
  const s = store(native(calls, { app_update_status: () => { if (fail) throw Error('offline'); return report('staging') } }))
  await s.prepareStartupUpdate()
  fail = false
  await s.checkAppUpdate()
  assert.equal(s.appUpdate.phase, 'staging')
  assert.deepEqual(commands(calls), ['app_update_status', 'app_update_status'])
})

test('cancel schedule is deduplicated and clears visible state only after native success', async () => {
  const calls = [], cancel = deferred(), s = store(native(calls, { cancel_scheduled_update: () => cancel.promise }))
  ready(s, 'scheduled'); s.openAppUpdate()
  const pending = s.cancelScheduledUpdate()
  await s.cancelScheduledUpdate()
  await s.stageAppUpdate()
  assert.equal(s.appUpdate.phase, 'scheduled')
  assert.ok(s.appUpdate.info)
  cancel.resolve()
  await pending
  assert.equal(s.appUpdate.phase, 'idle')
  assert.equal(s.appUpdate.info, null)
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.deepEqual(commands(calls), ['cancel_scheduled_update'])
})

test('failed cancellation preserves the schedule for retry', async () => {
  const calls = [], s = store(native(calls, { cancel_scheduled_update: () => { throw Error('cannot save') } }))
  ready(s, 'scheduled')
  await s.cancelScheduledUpdate()
  assert.equal(s.appUpdate.phase, 'scheduled')
  assert.equal(s.appUpdate.info.version, info.version)
  assert.match(s.appUpdate.error, /cannot save/)
})

for (const phase of ['scheduled', 'finished']) test(`clearing ${phase} never pretends to uninstall a version already on disk`, async () => {
  const calls = [], s = store(native(calls))
  ready(s, phase); s.appUpdate.info.installedLocally = true
  await s.cancelScheduledUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.appUpdate.info.version, info.version)
  assert.equal(s.appUpdate.info.installedLocally, true)
  await s.stageAppUpdate()
  assert.deepEqual(commands(calls), ['cancel_scheduled_update'])
})

test('partial recovery reports reasons, stays quiet in background and requires explicit retry', async () => {
  const calls = [], retry = deferred()
  let status = report('finished', { projects: [project], remaining: [project], failures: ['Project A: port occupied'] })
  const s = store(native(calls, { app_update_status: () => status, retry_update_restore: () => retry.promise }))
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'finished')
  assert.deepEqual(s.appUpdate.report.failures, ['Project A: port occupied'])
  assert.equal(s.appUpdate.dialogOpen, false)
  s.openAppUpdate()
  const pending = s.retryUpdateRestore()
  await s.retryUpdateRestore()
  assert.equal(s.appUpdate.phase, 'restoring')
  status = report('restoring', { projects: [project], remaining: [project] })
  retry.resolve()
  await pending
  s.dismissAppUpdate()
  status = report('finished')
  await s.tick()
  assert.equal(s.appUpdate.phase, 'finished')
  assert.equal(s.appUpdate.dialogOpen, false)
  assert.equal(s.timers.size, 0)
  assert.equal(commands(calls).filter(c => c === 'retry_update_restore').length, 1)
})

test('a restore retry failure keeps reasons and the retry action available', async () => {
  const calls = [], s = store(native(calls, {
    app_update_status: () => report('finished', { remaining: [project], failures: ['port occupied'] }),
    retry_update_restore: () => { throw Error('still occupied') },
  }))
  await s.prepareStartupUpdate()
  await s.retryUpdateRestore()
  assert.equal(s.appUpdate.phase, 'finished')
  assert.match(s.appUpdate.error, /still occupied/)
  assert.equal(s.appUpdate.report.remaining.length, 1)
})

test('completed failed transaction can be acknowledged and checked again explicitly', async () => {
  const calls = []
  let status = report('finished', { error: 'installation failed' })
  const s = store(native(calls, { app_update_status: () => status, cancel_scheduled_update: () => { status = null } }))
  await s.prepareStartupUpdate()
  await s.recheckAppUpdate()
  assert.equal(s.appUpdate.phase, 'available')
  assert.equal(s.appUpdate.dialogOpen, true)
  assert.deepEqual(commands(calls), ['app_update_status', 'cancel_scheduled_update', 'app_update_status', 'check_app_update'])
})

test('installed dialog makes acknowledgement primary and activation secondary without offering cancellation', async () => {
  const s = store(native([])); ready(s, 'installed')
  const html = await renderDialog(s)
  assert.match(html, /新版已安装，完整重启后生效。项目暂不受影响。/)
  assert.match(html, /保留项目退出只会更新窗口；后台更新需停止项目。/)
  assert.match(html, /<button>立即生效<\/button>/)
  assert.match(html, /<button class="primary">知道了<\/button>/)
  assert.doesNotMatch(html, /取消安排|自动恢复|暂停并恢复/)
})

test('reopening an updated shell with a retained old backend explains activation and never stops projects', async () => {
  const calls = []
  let value = report('staged', { version: '2.1.0', currentVersion: '2.1.0', backendVersion: '2.0.49', activationState: 'backend_pending' })
  const s = store(native(calls, { app_update_status: () => value }))
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'installed')
  assert.equal(s.installedUpdateCopy.value.title, '后台待更新')
  assert.equal(s.timers.size, 0)
  const html = await renderDialog(s)
  assert.match(html, /窗口已更新，项目继续运行；后台暂用旧版。/)
  assert.match(html, /后台版本.*2\.0\.49/)
  assert.match(html, /立即生效/)
  assert.doesNotMatch(html, /下次启动生效|更新完成|项目已恢复/)
  s.openAppUpdate(); await setImmediate()
  assert.equal(s.timers.size, 1)
  s.dismissAppUpdate()
  assert.equal(s.timers.size, 0)
  // Native verification retires the persisted record once both processes run the new version.
  value = null
  s.openAppUpdate(); await setImmediate()
  assert.equal(s.appUpdate.phase, 'current')
  assert.equal(s.appUpdate.info, null)
  assert.equal(s.appUpdate.report, null)
  assert.equal(s.timers.size, 0)
  assert.ok(commands(calls).every(cmd => cmd === 'app_update_status'))
})

test('unconfirmed retained backend does not claim pending restart or allow activation', async () => {
  const calls = [], s = store(native(calls, { app_update_status: () => report('staged', {
    currentVersion: info.version, backendVersion: null, activationState: 'unconfirmed',
  }) }))
  await s.refreshUpdateStatus({ background: true })
  const html = await renderDialog(s)
  assert.match(html, /状态待确认/)
  assert.doesNotMatch(html, /立即生效|下次启动生效|更新完成/)
  await s.planAppUpdate()
  assert.deepEqual(commands(calls), ['app_update_status'])
})

test('ready and legacy scheduled dialogs only offer silent installation', async () => {
  const s = store(native([]))
  for (const phase of ['ready', 'scheduled']) {
    ready(s, phase)
    const html = await renderDialog(s)
    assert.match(html, /<button class="primary">静默安装<\/button>/)
    assert.doesNotMatch(html, /立即生效|停止项目并安装|取消安排/)
    assert.equal(html.match(/<footer>(.*?)<\/footer>/s)[1].match(/<button\b/g).length, 1)
    assert.match(html, /项目保持运行。/)
  }
})

test('staging timeout dialog can be closed and has no install or retry button', async () => {
  const s = store(native([])); ready(s, 'staging'); s.appUpdate.error = 'installer still running'
  const html = await renderDialog(s)
  assert.match(html, /正在后台静默安装，可关闭此窗口。项目保持运行。/)
  assert.match(html, /installer still running/)
  assert.match(html, /<button class="primary">知道了<\/button>/)
  assert.doesNotMatch(html, /disabled|重试静默安装|立即生效|停止项目并/)
})

test('activation confirmation discloses session loss and only promises a restart attempt', async () => {
  const s = store(native([])); ready(s, 'installed')
  await s.planAppUpdate()
  const html = await renderDialog(s)
  assert.match(html, /将停止以下项目，超时会结束其进程。请先保存工作，调试会话不会保留。/)
  assert.match(html, /完成后尝试重新启动；失败会列出原因。/)
  assert.match(html, /Project A/)
  assert.match(html, /<button class="danger">停止项目并重启<\/button>/)
  assert.doesNotMatch(html, /自动恢复|无感|暂停并恢复/)
})

test('failed background check renders a visible error and retry without a fake version', async () => {
  const s = store(native([])); s.appUpdate.error = 'offline'
  const html = await renderDialog(s)
  assert.match(html, /offline/)
  assert.match(html, /<button class="primary">重新检查<\/button>/)
  assert.doesNotMatch(html, /class="version"|undefined/)
})
