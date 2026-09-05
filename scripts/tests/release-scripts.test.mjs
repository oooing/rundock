import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { copyFileSync, existsSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
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
function parse(dir, options = [], { tagName = 'v2.0.0', scriptRoot = root } = {}) {
  return exec(pwsh, ['-NoProfile', '-File', path.join(scriptRoot, 'scripts/release-plan.ps1'),
    '-TagName', tagName, '-OutputDirectory', path.join(dir, 'plan'), ...options], dir)
}
function readPlan(dir) {
  return JSON.parse(readFileSync(path.join(dir, 'plan/release-plan.json'), 'utf8'))
}

test('Windows and source-only dry-runs cannot publish and need no real tag', () => {
  // This smoke test validates the real checkout, so its tag must track the app version.
  const version = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8')).version
  for (const sourceOnly of [false, true]) {
    const dir = fixture(`dry-${sourceOnly}`)
    const result = parse(dir, ['-DryRun', '-ValidateLauncherVersion', ...(sourceOnly ? ['-SourceOnly'] : [])],
      { tagName: `v${version}` })
    assert.equal(result.status, 0, result.output)
    assert.equal(readPlan(dir).tagName, `v${version}`)
    assert.equal(readPlan(dir).targetVersion, version)
    assert.equal(readPlan(dir).publishesRelease, false)
    assert.equal(readPlan(dir).pushRemote, false)
    assert.equal(readPlan(dir).buildWindows, !sourceOnly)
    assert.equal(checked('git', ['tag', '--list'], dir), '')
  }
})

function versionFixture(name, version, overrides = {}) {
  const dir = fixture(name)
  const versions = {
    'package.json': version,
    'package-lock.json': version,
    'package-lock.json#packages-root': version,
    'src-tauri/tauri.conf.json': version,
    'src-tauri/Cargo.toml': version,
    ...overrides,
  }
  mkdirSync(path.join(dir, 'scripts'))
  mkdirSync(path.join(dir, 'src-tauri'))
  // The production script resolves version files relative to itself. Copy it into
  // the fixture so regression cases never rewrite the user's working checkout.
  copyFileSync(path.join(root, 'scripts/release-plan.ps1'), path.join(dir, 'scripts/release-plan.ps1'))
  writeFileSync(path.join(dir, 'package.json'), JSON.stringify({ version: versions['package.json'] }))
  writeFileSync(path.join(dir, 'package-lock.json'), JSON.stringify({
    version: versions['package-lock.json'],
    packages: { '': { version: versions['package-lock.json#packages-root'] } },
  }))
  writeFileSync(path.join(dir, 'src-tauri/tauri.conf.json'), JSON.stringify({ version: versions['src-tauri/tauri.conf.json'] }))
  writeFileSync(path.join(dir, 'src-tauri/Cargo.toml'), `[package]\nname = "release-test"\nversion = "${versions['src-tauri/Cargo.toml']}"\n`)
  copyFileSync(path.join(root, 'src-tauri/Cargo.lock'), path.join(dir, 'src-tauri/Cargo.lock'))
  return dir
}

test('version validation accepts matching initial, patch, minor and major versions', async t => {
  for (const version of ['2.0.0', '2.0.1', '2.7.13', '3.0.0']) {
    await t.test(version, () => {
      const dir = versionFixture(`version-${version}`, version)
      const result = parse(dir, ['-DryRun', '-ValidateLauncherVersion'],
        { tagName: `v${version}`, scriptRoot: dir })
      assert.equal(result.status, 0, result.output)
      assert.equal(readPlan(dir).targetVersion, version)
      assert.equal(readPlan(dir).publishesRelease, false)
      assert.equal(checked('git', ['tag', '--list'], dir), '')
    })
  }
})

test('version validation still rejects each mismatched version field', async t => {
  const fields = ['package.json', 'package-lock.json', 'package-lock.json#packages-root',
    'src-tauri/tauri.conf.json', 'src-tauri/Cargo.toml']
  for (const [index, field] of fields.entries()) {
    await t.test(field, () => {
      const dir = versionFixture(`version-mismatch-${index}`, '2.0.1', { [field]: '2.0.0' })
      const result = parse(dir, ['-DryRun', '-ValidateLauncherVersion'],
        { tagName: 'v2.0.1', scriptRoot: dir })
      assert.notEqual(result.status, 0, result.output)
      assert.match(result.output, /release_plan_invalid/)
      assert.ok(result.output.includes(`${field}=2.0.0`), result.output)
      assert.equal(existsSync(path.join(dir, 'plan/release-plan.json')), false)
    })
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

test('RunDock cloud target uses the action accepted by the publisher', () => {
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

test('RunDock branding preserves application and MSI upgrade identities', () => {
  const config = JSON.parse(readFileSync(path.join(root, 'src-tauri/tauri.conf.json'), 'utf8'))
  assert.equal(config.productName, 'RunDock')
  assert.equal(config.identifier, 'com.launcher.platform')
  assert.equal(config.bundle.windows.wix.upgradeCode, '3dab43c3-d2d7-5eba-adb3-07bfe05728bb')
  assert.equal(config.app.windows[0].title, 'RunDock 启动坞')
  const hooks = readFileSync(path.join(root, 'src-tauri', config.bundle.windows.nsis.installerHooks), 'utf8')
  assert.match(hooks, /Uninstall\\Launcher/)
  assert.match(hooks, /Abort/)
  const release = JSON.parse(readFileSync(path.join(root, '.launcher/release.yaml'), 'utf8'))
  assert.deepEqual(release.targets[0].artifacts, ['RunDock_X.Y.Z_x64-setup.exe', 'RunDock_X.Y.Z_x64_en-US.msi', 'SHA256SUMS.txt'])
})

function publishFixture(name, existing = null) {
  const dir = fixture(name)
  tag(dir, plan())
  const notes = path.join(dir, 'notes.md')
  writeFileSync(notes, '## 功能\n- 发布验收。\n')
  const assets = path.join(dir, 'assets')
  mkdirSync(assets)
  for (const name of ['RunDock_2.0.0_x64-setup.exe', 'RunDock_2.0.0_x64_en-US.msi', 'SHA256SUMS.txt']) {
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
