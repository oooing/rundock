import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as vue from 'vue'

// Exercise the current controller modules, not the old monolithic modal.
export function load(file, dependencies) {
  const source = readFileSync(new URL(`../../../src/${file}.ts`, import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '')
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('exports', ...Object.keys(dependencies), compiled)(exports, ...Object.values(dependencies))
  return exports
}
const pure = { ...load('utils/releaseContent', {}), ...load('utils/releasePresentation', {}), ...load('utils/releaseSafety', {}),
  ...load('utils/releasePreferences', { localStorage: { getItem: () => null, setItem() {} } }) }
export function fixture({ local = false, single = false, api = {}, preferences = {}, preferenceStorage, realVersionSync = false, realNotes = false } = {}) {
  const requests = []
  const deps = {
    ref: vue.ref, computed: vue.computed, watch: vue.watch, nextTick: vue.nextTick,
    ...pure, tr: (text, args = []) => text.replace(/\{(\d+)\}/g, (_, n) => args[n] ?? ''),
    onMounted() {}, onBeforeUnmount() {}, rememberReleaseSession() {},
    navigator: { platform: 'Win32', userAgent: '' },
    api: { async saveReleaseProfile(_, body) { return body }, async createRelease(_, body) { requests.push(body); return { id: 'captured' } }, ...api },
    readReleasePreferences: () => preferences,
    ...(preferenceStorage ? load('utils/releasePreferences', { localStorage: preferenceStorage }) : {}),
  }
  const ctx = { props: { app: { id: 'fixture', name: 'Fixture', status: 'stopped' } }, emit() {} }
  const scope = vue.effectScope()
  scope.run(() => {
    for (const [file, installer] of [['state', 'installState'], ['candidate', 'installCandidate'], ['selection', 'installSelection'],
      ['platforms', 'installPlatforms'], ['configuration', 'installConfiguration'], ['sync', 'installSync'],
      ['preferences', 'installPreferences'], ['submission', 'installSubmission'], ['notes', 'installNotes']]) {
      load(`components/release/${file}`, deps)[installer](ctx)
    }
    if (!realNotes) ctx.scheduleReleaseNotesDraft = () => {}
    if (!realVersionSync) ctx.syncVersionInputs = () => {}
    ctx.showRun = () => {}
    ctx.savePreparation = async () => {}
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
  return { ...ctx, requests, model: ctx, close: () => { ctx.cancelReleaseNotesDraft(); scope.stop() } }
}

