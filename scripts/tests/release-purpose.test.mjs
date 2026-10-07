import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import * as vue from 'vue'

// Exercise the current controller modules, not the old monolithic modal.
function load(file, dependencies) {
  const source = readFileSync(new URL(`../../src/${file}.ts`, import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '')
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('exports', ...Object.keys(dependencies), compiled)(exports, ...Object.values(dependencies))
  return exports
}
const pure = { ...load('utils/releaseContent', {}), ...load('utils/releasePresentation', {}),
  ...load('utils/releasePreferences', { localStorage: { getItem: () => null, setItem() {} } }) }
function fixture({ local = false, single = false } = {}) {
  const requests = []
  const deps = {
    ref: vue.ref, computed: vue.computed, watch: vue.watch, nextTick: vue.nextTick,
    ...pure, tr: (text, args = []) => text.replace(/\{(\d+)\}/g, (_, n) => args[n] ?? ''),
    onMounted() {}, onBeforeUnmount() {}, rememberReleaseSession() {},
    navigator: { platform: 'Win32', userAgent: '' },
    api: { async saveReleaseProfile(_, body) { return body }, async createRelease(_, body) { requests.push(body); return { id: 'captured' } } },
  }
  const ctx = { props: { app: { id: 'fixture', name: 'Fixture', status: 'stopped' } }, emit() {} }
  const scope = vue.effectScope()
  scope.run(() => {
    for (const [file, installer] of [['state', 'installState'], ['candidate', 'installCandidate'], ['selection', 'installSelection'],
      ['platforms', 'installPlatforms'], ['configuration', 'installConfiguration'], ['sync', 'installSync'],
      ['preferences', 'installPreferences'], ['submission', 'installSubmission']]) {
      load(`components/release/${file}`, deps)[installer](ctx)
    }
    ctx.scheduleReleaseNotesDraft = () => {}
    ctx.syncVersionInputs = () => {}
    ctx.showRun = () => {}
    ctx.buildMode.value = local ? 'local' : 'github'
    ctx.preflight.value = ctx.normalizePreflight({ canRelease: true, repoRoot: 'C:/fixture', branch: 'main',
      remoteUrl: 'https://github.com/fixture/project.git', remotes: ['origin'], statusFingerprint: 'current',
      latestTag: 'v1.0.0', suggestedVersion: '1.0.1', currentVersions: {}, versionFiles: [],
      changes: [{ path: 'src/main.ts', status: ' M', tracked: true }], blockingIssues: [],
    })
    const ids = single ? ['desktop'] : ['desktop', 'android']
    ctx.applyReleaseConfig({ schemaVersion: 1, source: 'file', versionGroups: ids.map(id => ({ id, name: id, currentVersion: '1.0.0', versionFiles: [] })),
      targets: ids.map(id => ({ id, name: id, kind: id, versionGroup: id, enabled: true,
        runner: { type: local ? 'local' : 'git-push', os: ['windows'] },
        steps: local ? { build: 'build', package: 'package' } : { publish: 'tag-push' }, artifacts: ['output.zip'],
      })), warnings: [],
    })
    for (const choice of Object.values(ctx.targetChoices.value)) choice.selected = true
    ctx.selected.value = { 'src/main.ts': true }
    ctx.releaseNotes.value = 'Release notes'
    ctx.commitMessage.value = 'Update fixture'
    load('components/release/lifecycle', deps).installLifecycle(ctx)
  })
  return { ...ctx, requests, close: () => scope.stop() }
}

test('cloud and local counterparts share platform identity, copy, icons and order without changing runners', () => {
  const f = fixture()
  try {
    const products = [ ['android', 'Android'], ['server', 'Web + 服务器后端'], ['desktop', 'PC 客户端'], ['extension', '浏览器扩展'] ]
    const targets = products.flatMap(([kind, name]) => ['github', 'local'].map(mode => ({
      id: `${kind}-${mode}`, kind, name: `${name} · ${mode === 'local' ? '本地' : '云端'}`, versionGroup: kind,
      enabled: true, runner: { type: mode === 'local' ? 'local' : 'git-push', os: mode === 'local' ? ['windows'] : [] },
      steps: mode === 'local' ? { build: 'build', package: 'package' } : { publish: 'tag-push' }, artifacts: ['out.zip'],
    })))
    f.applyReleaseConfig({ schemaVersion: 1, source: 'file', targets, versionGroups: products.map(([id]) => ({ id, name: id, currentVersion: '1.0.0', versionFiles: [] })), warnings: [] })
    const snapshot = () => f.visibleProductPlatforms.value.map(p => ({ id: p.id, name: p.name, icon: p.icon, detail: f.platformCardDetail(p) }))
    const cloud = snapshot()
    assert.deepEqual(cloud.map(p => p.name), ['PC 客户端', 'Android', 'Web + 服务器后端', '浏览器扩展'])
    assert.equal(cloud[0].icon, '🖥️')
    assert.equal(cloud[0].detail, 'Windows 桌面端')
    for (const p of f.visibleProductPlatforms.value) f.togglePlatform(p, true)
    assert.deepEqual(f.selectedTargets.value.map(t => t.targetId).sort(), products.map(([id]) => `${id}-github`).sort())
    f.changeBuildMode('local')
    assert.deepEqual(snapshot(), cloud)
    assert.deepEqual(f.selectedTargets.value.map(t => t.targetId).sort(), products.map(([id]) => `${id}-local`).sort())
    f.changeBuildMode('github')
    assert.deepEqual(snapshot(), cloud)
    assert.deepEqual(f.selectedTargets.value.map(t => t.targetId).sort(), products.map(([id]) => `${id}-github`).sort())
    assert.deepEqual(f.configuredTargets.value.find(t => t.id === 'desktop-github').runner.os, [], 'display inference must not rewrite configuration')
    f.configuredTargets.value.find(t => t.id === 'desktop-github').runner.os = ['any']
    assert.deepEqual(snapshot(), cloud, 'an explicit any OS uses the same unambiguous counterpart')
  } finally { f.close() }
})

test('ambiguous desktop counterparts do not turn a multi-OS cloud build into Windows', () => {
  const f = fixture()
  try {
    const targets = [ ['cloud', 'git-push', []], ['win', 'local', ['windows']], ['mac', 'local', ['darwin']] ].map(([id, type, os]) => ({
      id, name: 'Desktop', kind: 'desktop', versionGroup: 'desktop', enabled: true,
      runner: { type, os }, steps: type === 'local' ? { build: 'build' } : { publish: 'tag-push' },
    }))
    f.applyReleaseConfig({ schemaVersion: 1, source: 'file', targets, versionGroups: [{ id: 'desktop', name: 'Desktop', versionFiles: [] }], warnings: [] })
    assert.equal(f.platformIdForTarget(f.configuredTargets.value[0]), 'custom:cloud')
    assert.equal(f.platformIdForTarget(f.configuredTargets.value[1]), 'pc')
    assert.equal(f.platformIdForTarget(f.configuredTargets.value[2]), 'mac')
  } finally { f.close() }
})

test('code-only requests omit builds, versions and tags for both remembered build modes', async () => {
  for (const local of [false, true]) {
    const f = fixture({ local })
    try {
      f.changeReleaseIntent('save-progress')
      f.createTag.value = true; f.gitOnly.value = false // Late preference response.
      const request = f.candidateRequest.value
      assert.equal(request.intent, 'save-progress')
      assert.equal(request.createTag, false)
      assert.equal(request.targetVersion, '')
      assert.deepEqual(request.versions, [])
      assert.deepEqual(request.selectedTargets, [])
      assert.equal(request.buildMode, 'none')
      assert.equal(request.pushRemote, true, 'keep the explicit GitHub sync choice')
      assert.deepEqual(f.chosenTargets.value, [])
      assert.equal(f.selectedNeedsRemotePush.value, false)
      assert.equal(f.hasExternalAction.value, false)
      assert.equal(f.syncDeliveryMissing.value, false)
      assert.equal(f.targetSelectionValid.value, true)
      assert.equal(f.canSubmit.value, true)
      f.candidate.value = { id: 'checked', accepted: true, canSaveProgress: true, status: 'ready' }
      f.candidateSignature.value = f.currentCandidateSignature.value
      await f.publish()
      assert.equal(f.requests.length, 1)
      assert.equal(f.requests[0].buildMode, 'none')
      assert.deepEqual(f.requests[0].selectedTargets, [])
      assert.equal(f.requests[0].releaseNotes, '')
      assert.equal(f.requests[0].externalActionsConfirmed, false)
    } finally { f.close() }
  }
})

test('switching purposes preserves selected platforms, manual versions, notes and files', () => {
  const f = fixture()
  try {
    f.versionMode.value = 'manual'
    f.versionInputs.value = { desktop: '2.0.0', android: '3.0.0' }
    f.releaseNotesDirty.value = true
    const choices = JSON.stringify(f.targetChoices.value)
    f.changeReleaseIntent('save-progress'); f.changeReleaseIntent('formal')
    assert.equal(f.createTag.value, true)
    assert.equal(f.gitOnly.value, false)
    assert.equal(f.versionMode.value, 'manual')
    assert.deepEqual(f.versionInputs.value, { desktop: '2.0.0', android: '3.0.0' })
    assert.equal(JSON.stringify(f.targetChoices.value), choices)
    assert.equal(f.selectedTargets.value.length, 2)
    assert.equal(f.releaseNotes.value, 'Release notes')
    assert.deepEqual(f.selectedPaths.value, ['src/main.ts'])
  } finally { f.close() }
})

test('multiple platforms require a choice; a sole platform can be selected automatically', () => {
  for (const single of [false, true]) {
    const f = fixture({ single })
    try {
      for (const choice of Object.values(f.targetChoices.value)) choice.selected = false
      f.changeReleaseIntent('save-progress'); f.changeReleaseIntent('formal')
      assert.equal(f.targetSelectionMissing.value, !single)
      assert.equal(f.selectedTargets.value.length, single ? 1 : 0)
    } finally { f.close() }
  }
})

test('code-only ignores build/version configuration failures but retains Git safety checks', () => {
  const f = fixture()
  try {
    f.preflight.value.canRelease = false
    f.preflight.value.blockingIssues = [{ code: 'release_config_invalid' }, { code: 'version_file_invalid' }]
    assert.equal(f.canSubmit.value, false)
    f.changeReleaseIntent('save-progress')
    assert.equal(f.canSubmit.value, true)
    f.preflight.value.blockingIssues.push({ code: 'merge_conflict' })
    assert.equal(f.canSubmit.value, false)
  } finally { f.close() }
})

test('code-only local destination does not require remote builds or mention packages', () => {
  const f = fixture()
  try {
    f.changeReleaseIntent('save-progress'); f.changeSyncPolicy('local')
    assert.equal(f.pushRemote.value, false)
    assert.equal(f.targetSelectionValid.value, true)
    assert.equal(f.candidateRequest.value.buildMode, 'none')
    assert.equal(f.syncNotice.value, '代码只保存在本机，不推送。')
  } finally { f.close() }
})

test('checking and submission lock purpose; choosing the active purpose is a no-op', () => {
  for (const flag of ['checkingCandidate', 'publishing', 'autoSubmitting']) {
    const f = fixture()
    try { f[flag].value = true; f.changeReleaseIntent('save-progress'); assert.equal(f.releaseIntent.value, 'formal') }
    finally { f.close() }
  }
  const f = fixture()
  try { f.versionMode.value = 'manual'; f.changeReleaseIntent('formal'); assert.equal(f.versionMode.value, 'manual'); assert.equal(f.editedReleaseOptions.size, 0) }
  finally { f.close() }
})

test('purpose is first and remains accessible during current-version local builds', () => {
  const source = readFileSync(new URL('../../src/components/ReleaseModal.vue', import.meta.url), 'utf8')
  assert.ok(source.indexOf('<fieldset class="publish-purpose"') < source.indexOf('<ReleaseBuildChoice'))
  assert.ok(source.indexOf('name="release-intent" value="formal"') < source.indexOf('name="release-intent" value="save-progress"'))
  assert.ok(source.indexOf('<fieldset class="publish-purpose"') < source.indexOf('<template v-if="!localBuildVisible'))
  assert.match(source, /releaseIntent==='formal' && !targetSelectionMissing" class="block release-versions"/)
  assert.match(source, /:cloud-build="releaseIntent==='formal' && buildMode==='github'"/)
  assert.ok(source.indexOf('<ReleaseBuildChoice') < source.indexOf('<ReleaseSyncChoice'))
  assert.match(source, /:plan="buildPlan".*@select="changeBuildPlan"/)
  assert.match(source, /<ReleaseSyncChoice v-else code-only/)
  assert.ok(source.indexOf('<ReleaseSyncChoice') < source.indexOf('<template v-if="!localBuildVisible'))
  assert.doesNotMatch(source, /buildCurrentVersion|v-model:current-only/)
  assert.doesNotMatch(source, /local-version-choice|<ReleaseBuildModeChoice|#local-options/)
  const buildChoice = readFileSync(new URL('../../src/components/ReleaseBuildChoice.vue', import.meta.url), 'utf8')
  assert.doesNotMatch(buildChoice, /type="checkbox"|currentOnly/)
  assert.match(buildChoice, /v-if="open" id="local-version-cascade"/)
  assert.match(buildChoice, /:disabled="disabled"/)
})

test('all card/cascade transitions map to the existing runner, upload and version policy without creating tasks', () => {
  const plans = ['cloud', 'local-publish', 'local-current', 'local-upgrade']
  for (const from of plans) for (const to of plans) {
    const f = fixture()
    try {
      f.changeBuildPlan(from)
      f.changeBuildPlan(to)
      assert.equal(f.buildPlan.value, to)
      assert.equal(f.buildMode.value, to === 'cloud' ? 'github' : 'local')
      assert.equal(f.syncPolicy.value, ['cloud', 'local-publish'].includes(to) ? 'auto' : 'local')
      assert.equal(f.pushRemote.value, ['cloud', 'local-publish'].includes(to))
      assert.equal(f.localBuildOnly.value, to === 'local-current')
      if (to.startsWith('local-') && to !== 'local-publish')
        assert.equal(f.localVersionMode.value, to === 'local-upgrade' ? 'upgrade' : 'current')
      assert.deepEqual(f.requests, [])
    } finally { f.close() }
  }
})

test('card selection is locked while checking/submitting or in code-only mode', () => {
  for (const flag of ['checkingCandidate', 'publishing', 'autoSubmitting', 'codeOnly']) {
    const f = fixture()
    try {
      if (flag === 'codeOnly') f.changeReleaseIntent('save-progress')
      else f[flag].value = true
      f.changeBuildPlan('local-upgrade')
      assert.equal(f.buildPlan.value, 'cloud')
      assert.equal(f.localVersionMode.value, 'current')
      assert.deepEqual(f.requests, [])
    } finally { f.close() }
  }
})

test('card and cascade copy has English translations, including dynamic option labels', () => {
  const dictionary = JSON.parse(readFileSync(new URL('../../src/i18n/en.json', import.meta.url), 'utf8'))
  const source = readFileSync(new URL('../../src/components/ReleaseBuildChoice.vue', import.meta.url), 'utf8')
  const script = source.split('<script setup lang="ts">')[1].split('</script>')[0]
  const ast = ts.createSourceFile('build-choice.ts', script, ts.ScriptTarget.Latest, true)
  const keys = []
  function visit(node) {
    if (ts.isStringLiteral(node) && /[\u3400-\u9fff]/.test(node.text)) keys.push(node.text)
    ts.forEachChild(node, visit)
  }
  visit(ast)
  for (const match of source.matchAll(/tr\('([^']+)'\)/g)) keys.push(match[1])
  assert.ok(keys.length >= 12)
  for (const key of keys) assert.ok(dictionary[key], `missing translation: ${key}`)
})

test('local destination defaults to isolated packaging and cannot submit a release', async () => {
  const f = fixture()
  try {
    f.changeSyncPolicy('local')
    assert.equal(f.buildMode.value, 'local')
    assert.equal(f.localBuildOnly.value, true)
    assert.equal(f.pushRemote.value, false)
    assert.equal(f.canSubmit.value, false)
    assert.match(f.syncNotice.value, /不提交代码、不上传/)
    f.candidate.value = { id: 'old-check', accepted: true, status: 'ready' }
    f.candidateSignature.value = f.currentCandidateSignature.value
    await f.submitRelease()
    await f.publish()
    assert.deepEqual(f.requests, [], 'packaging must never call the release/commit endpoint')
    f.changeBuildMode('github')
    assert.equal(f.buildMode.value, 'github', 'explicit cloud selection enables the cloud release path')
    assert.equal(f.syncPolicy.value, 'auto')
    assert.equal(f.localSyncPolicy.value, 'local', 'cloud selection must not erase the local delivery preference')
    assert.equal(f.pushRemote.value, true)
    f.changeBuildMode('local')
    assert.equal(f.syncPolicy.value, 'local')
    assert.equal(f.localBuildOnly.value, true)
    assert.equal(f.pushRemote.value, false)
    assert.deepEqual(f.requests, [], 'changing build location never starts a build or release')
  } finally { f.close() }
})

test('local upgrade is explicit, creates a version, and disables upload and deployment', () => {
  const f = fixture({ local: true })
  try {
    f.changeSyncPolicy('local')
    assert.equal(f.localVersionMode.value, 'current')
    f.changeLocalVersionMode('upgrade')
    assert.equal(f.localBuildOnly.value, false)
    assert.equal(f.canSubmit.value, true)
    const request = f.candidateRequest.value
    assert.equal(request.createTag, true)
    assert.equal(request.buildMode, 'local')
    assert.equal(request.pushRemote, false)
    assert.equal(request.versions.length, 2)
    assert.ok(request.selectedTargets.every(t => t.build && !t.publish && !t.deploy))
    f.changeLocalVersionMode('current')
    assert.equal(f.canSubmit.value, false)
  } finally { f.close() }
})

test('GitHub delivery retains both build methods and local version choice does not affect code-only', () => {
  const f = fixture({ local: true })
  try {
    f.changeSyncPolicy('local')
    f.changeReleaseIntent('save-progress')
    assert.equal(f.localBuildOnly.value, false)
    assert.equal(f.canSubmit.value, true)
    assert.equal(f.candidateRequest.value.buildMode, 'none')
    f.changeReleaseIntent('formal')
    assert.equal(f.localBuildOnly.value, true)
    f.changeSyncPolicy('auto')
    assert.equal(f.localBuildOnly.value, false)
    assert.equal(f.buildMode.value, 'local')
    assert.equal(f.pushRemote.value, true)
    f.changeBuildMode('github')
    assert.equal(f.buildMode.value, 'github')
    assert.equal(f.syncPolicy.value, 'auto')
    f.changeBuildMode('local')
    assert.equal(f.syncPolicy.value, 'auto')
  } finally { f.close() }
})

test('old cloud/local preferences normalize safely, and missing GitHub cannot silently release locally', () => {
  const f = fixture()
  try {
    f.syncPolicy.value = 'local'
    f.applySyncPolicy()
    assert.equal(f.buildMode.value, 'local')
    assert.equal(f.localBuildOnly.value, true)
    f.preflight.value.remoteUrl = ''
    f.changeSyncPolicy('auto')
    assert.equal(f.canSubmit.value, false)
    assert.match(f.syncNotice.value, /未检测到 GitHub/)
    f.changeReleaseIntent('save-progress')
    assert.equal(f.canSubmit.value, false)
    f.changeSyncPolicy('local')
    assert.equal(f.canSubmit.value, true)
  } finally { f.close() }
})

test('destination, local version and build method are locked while checking or submitting', () => {
  for (const flag of ['checkingCandidate', 'publishing', 'autoSubmitting']) {
    const f = fixture()
    try {
      f[flag].value = true
      f.changeSyncPolicy('local'); f.changeLocalVersionMode('upgrade'); f.changeBuildMode('local')
      assert.equal(f.syncPolicy.value, 'auto')
      assert.equal(f.localSyncPolicy.value, 'auto')
      assert.equal(f.localVersionMode.value, 'current')
      assert.equal(f.buildMode.value, 'github')
    } finally { f.close() }
  }
})
