import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const tempRoot = path.join(root, '.tmp', 'release-script-tests')
mkdirSync(tempRoot, { recursive: true })
const runRoot = mkdtempSync(path.join(tempRoot, 'run-'))
const pwsh = process.env.RELEASE_TEST_PWSH || 'pwsh'
const baseEnv = { ...process.env, GITHUB_OUTPUT: '', GH_TOKEN: '', GIT_TERMINAL_PROMPT: '0' }

function exec(command, args, cwd, env = {}) {
  const result = spawnSync(command, args, {
    cwd, env: { ...baseEnv, ...env }, encoding: 'utf8', timeout: 60_000, windowsHide: true,
  })
  if (result.error) throw result.error
  return { ...result, output: `${result.stdout}\n${result.stderr}` }
}
function checked(command, args, cwd) {
  const result = exec(command, args, cwd)
  assert.equal(result.status, 0, result.output)
  return result.stdout.trim()
}
function fixture(name) {
  const dir = path.join(runRoot, name)
  mkdirSync(dir)
  checked('git', ['init', '-b', 'v2'], dir)
  checked('git', ['config', 'user.email', 'release-test@example.invalid'], dir)
  checked('git', ['config', 'user.name', 'Release script test'], dir)
  checked('git', ['-c', 'core.hooksPath=', 'commit', '--allow-empty', '-m', 'test fixture'], dir)
  return dir
}
function plan(overrides = {}) {
  return {
    schemaVersion: 1, tagName: 'v2.0.0', targetVersion: '2.0.0', versionGroupId: 'product',
    pushRemote: true, publishesRelease: true,
    targets: [{ id: 'windows', kind: 'desktop', build: true, package: true, publish: false, deploy: false }],
    ...overrides,
  }
}
function tag(dir, metadata, notes = '## 功能\n- 支持项目发布。') {
  const encoded = Buffer.from(JSON.stringify(metadata)).toString('base64url')
  const message = `Release v2.0.0\n\n${notes}\n\n<!-- launcher-release-plan:${encoded} -->`
  checked('git', ['-c', 'tag.gpgSign=false', 'tag', '-a', 'v2.0.0', '--cleanup=verbatim', '-m', message], dir)
}
function parse(dir, options = []) {
  return exec(pwsh, ['-NoProfile', '-File', path.join(root, 'scripts/release-plan.ps1'),
    '-TagName', 'v2.0.0', '-OutputDirectory', path.join(dir, 'plan'), ...options], dir)
}
function readPlan(dir) {
  return JSON.parse(readFileSync(path.join(dir, 'plan/release-plan.json'), 'utf8'))
}

test('Windows and source-only dry-runs cannot publish and need no real tag', () => {
  for (const sourceOnly of [false, true]) {
    const dir = fixture(`dry-${sourceOnly}`)
    const result = parse(dir, ['-DryRun', '-ValidateLauncherVersion', ...(sourceOnly ? ['-SourceOnly'] : [])])
    assert.equal(result.status, 0, result.output)
    assert.equal(readPlan(dir).publishesRelease, false)
    assert.equal(readPlan(dir).pushRemote, false)
    assert.equal(readPlan(dir).buildWindows, !sourceOnly)
    assert.equal(checked('git', ['tag', '--list'], dir), '')
  }
})

test('annotated Windows tag restores the frozen plan and Chinese notes', () => {
  const dir = fixture('windows')
  tag(dir, plan())
  const result = parse(dir)
  assert.equal(result.status, 0, result.output)
  assert.equal(readPlan(dir).buildWindows, true)
  assert.equal(readPlan(dir).publishesRelease, true)
  assert.equal(readFileSync(path.join(dir, 'plan/release-notes.md'), 'utf8').replaceAll('\r\n', '\n').trim(), '## 功能\n- 支持项目发布。')
})

test('source-only tag does not request Windows installers', () => {
  const dir = fixture('source')
  tag(dir, plan({ targets: [] }))
  const result = parse(dir)
  assert.equal(result.status, 0, result.output)
  assert.equal(readPlan(dir).sourceOnly, true)
  assert.equal(readPlan(dir).buildWindows, false)
})

test('lightweight tags and annotated tags without a plan are blocked', () => {
  for (const annotated of [false, true]) {
    const dir = fixture(`missing-plan-${annotated}`)
    checked('git', ['-c', 'tag.gpgSign=false', 'tag', ...(annotated ? ['-a', '-m', 'Release v2.0.0'] : []), 'v2.0.0'], dir)
    assert.notEqual(parse(dir).status, 0)
  }
})

test('mismatched, unsupported and non-boolean plans are blocked', () => {
  for (const [name, metadata] of [
    ['tag', plan({ tagName: 'v2.0.1' })],
    ['boolean', plan({ publishesRelease: 'true' })],
    ['remote', plan({ pushRemote: false })],
    ['target', plan({ targets: [{ id: 'android', kind: 'android', build: true, package: false, publish: false, deploy: false }] })],
  ]) {
    const dir = fixture(`invalid-${name}`)
    tag(dir, metadata)
    assert.notEqual(parse(dir).status, 0, `${name} was accepted`)
  }
})

test('production workflow keeps dry-runs outside the publish job', () => {
  const workflow = readFileSync(path.join(root, '.github/workflows/release.yml'), 'utf8')
  assert.match(workflow, /github\.event_name == 'push'/)
  assert.match(workflow, /needs\.prepare\.outputs\.publishes_release == 'true'/)
  assert.match(workflow, /persist-credentials: false/)
  assert.doesNotMatch(workflow, /git fetch/)
})

test('Launcher cloud target uses the action accepted by the publisher', () => {
  const config = JSON.parse(readFileSync(path.join(root, '.launcher/release.yaml'), 'utf8'))
  assert.equal(config.automation.provider, 'github-actions')
  assert.equal(config.automation.trigger, 'tag')
  for (const target of config.targets.filter(target => target.enabled)) {
    assert.equal(target.runner.type, 'git-push')
    assert.equal(target.steps.publish, 'tag-push')
    for (const localAction of ['check', 'build', 'package', 'deploy']) {
      assert.ok(!target.steps[localAction], `cloud target must not request ${localAction}`)
    }
  }
})

test('release configuration and workflow follow the migrated main branch', () => {
  const config = JSON.parse(readFileSync(path.join(root, '.launcher/release.yaml'), 'utf8'))
  const workflow = readFileSync(path.join(root, '.github/workflows/release.yml'), 'utf8')
  assert.equal(config.automation.releaseBranch, 'master')
  assert.match(workflow, /git show-ref --verify --quiet refs\/remotes\/origin\/master/)
  assert.match(workflow, /git merge-base --is-ancestor \$tagCommit refs\/remotes\/origin\/master/)
  assert.match(workflow, /\$env:REF_NAME -ne 'master'/)
  assert.doesNotMatch(workflow, /refs\/remotes\/origin\/v2|\$env:REF_NAME -ne 'v2'/)
})

function publishFixture(name, existing = null) {
  const dir = fixture(name)
  tag(dir, plan())
  const notes = path.join(dir, 'notes.md')
  writeFileSync(notes, '## 功能\n- 发布验收。\n')
  const assets = path.join(dir, 'assets')
  mkdirSync(assets)
  for (const name of ['Launcher_2.0.0_x64-setup.exe', 'Launcher_2.0.0_x64_en-US.msi', 'SHA256SUMS.txt']) {
    writeFileSync(path.join(assets, name), 'isolated test artifact')
  }
  const state = path.join(dir, 'github-mock.json')
  writeFileSync(state, JSON.stringify({ release: existing, calls: [] }))
  const run = (sourceOnly = false) => exec(pwsh, ['-NoProfile', '-File',
    path.join(root, 'scripts/tests/publish-with-mock.ps1'), '-TagName', 'v2.0.0',
    '-NotesFile', notes, '-AssetDirectory', assets, ...(sourceOnly ? ['-SourceOnly'] : [])], dir,
  { RELEASE_TEST_STATE: state })
  return { dir, run, state: () => JSON.parse(readFileSync(state, 'utf8')) }
}

test('draft is public only after all assets have been uploaded and verified', () => {
  const fixture = publishFixture('publish-draft')
  const result = fixture.run()
  assert.equal(result.status, 0, result.output)
  const { release, calls } = fixture.state()
  assert.equal(release.isDraft, false)
  assert.equal(release.assets.length, 3)
  const upload = calls.findIndex(call => call[1] === 'upload')
  const publish = calls.findIndex(call => call.includes('--draft=false'))
  assert.ok(upload > 0 && publish > upload)
  assert.ok(calls.slice(upload + 1, publish).some(call => call[1] === 'view'))
  assert.equal(fixture.run().status, 0, 'a complete public release should be idempotent')
  assert.equal(fixture.state().calls.filter(call => call[1] === 'upload').length, 1)
})

test('an incomplete public release is never overwritten', () => {
  const fixture = publishFixture('publish-conflict', { isDraft: false, url: 'https://example.invalid/release', assets: [] })
  const result = fixture.run()
  assert.notEqual(result.status, 0)
  assert.match(result.output, /release_conflict/)
  assert.ok(fixture.state().calls.every(call => call[1] === 'view'))
})

test('source-only publishing does not upload installers', () => {
  const fixture = publishFixture('publish-source')
  const result = fixture.run(true)
  assert.equal(result.status, 0, result.output)
  assert.equal(fixture.state().release.isDraft, false)
  assert.ok(fixture.state().calls.every(call => call[1] !== 'upload'))
})
