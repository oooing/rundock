import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

function load(file, dependencies = {}) {
  const source = readFileSync(new URL(`../../src/utils/${file}.ts`, import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '')
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('exports', ...Object.keys(dependencies), compiled)(exports, ...Object.values(dependencies))
  return exports
}
const { releaseTargetLabel } = load('releasePresentation')
const { getReleaseSetupSummary } = load('releaseSetup', { tr: value => value, releaseTargetLabel })
const target = (id, name, runner = 'local', overrides = {}) => ({
  id, name, kind: 'desktop', versionGroup: 'product', workingDir: '.',
  enabled: true, runner: { type: runner, os: ['windows'] },
  steps: runner === 'local' ? { build: 'npm run build' } : { publish: 'workflow-dispatch:release.yml' },
  artifacts: runner === 'local' ? ['dist/*.exe'] : [], ...overrides,
})
const config = targets => ({ targets, versionGroups: [{ id: 'product', name: '产品' }, { id: 'other', name: '其他产品' }], automation: { provider: 'github-actions', trigger: 'dispatch', account: 'fixture', workflow: 'release.yml' } })

test('RunDock cloud and local PC definitions form one configuration summary', () => {
  const input = config([target('windows', 'Windows 安装包', 'git-push'), target('local-windows', 'Windows 安装包 · 本地')])
  const original = JSON.stringify(input)
  const rows = getReleaseSetupSummary(input)
  assert.equal(rows.length, 1)
  assert.equal(rows[0].name, 'Windows 安装包')
  assert.equal(rows[0].local.state, 'configured')
  assert.equal(rows[0].cloud.state, 'configured')
  assert.equal(rows[0].local.reason, '已配置')
  assert.equal(JSON.stringify(input), original, 'summary must not alter the configuration')
})

test('LocalPlay retains separate products and pairs an unspecified cloud OS with its only local variant', () => {
  const products = [ ['desktop', 'PC 客户端'], ['android', 'Android'], ['server', 'Web＋服务器后端'], ['custom', '浏览器扩展'] ]
  const rows = getReleaseSetupSummary(config(products.flatMap(([kind, name]) => [
    target(`${kind}-cloud`, `${name} · 云端`, 'git-push', { kind, versionGroup: kind, runner: { type: 'git-push', os: ['any'] } }),
    target(`${kind}-local`, `${name} · 本地`, 'local', { kind, versionGroup: kind }),
  ])))
  assert.deepEqual(rows.map(row => row.name), products.map(([, name]) => name))
  assert.ok(rows.every(row => row.local.state === 'configured' && row.cloud.state === 'configured'))
})

test('disabled, absent, and incomplete modes are distinct', () => {
  const rows = getReleaseSetupSummary(config([
    target('disabled', '停用端', 'local', { enabled: false }),
    target('no-artifacts', '未填写输出', 'local', { artifacts: [' '] }),
    target('no-command', '未填写命令', 'local', { steps: { check: 'npm test' } }),
    target('no-workflow', '未填写流程', 'git-push', { steps: { publish: 'workflow-dispatch:' } }),
  ]))
  assert.equal(rows[0].local.state, 'disabled')
  assert.equal(rows[0].cloud.state, 'missing')
  assert.deepEqual(rows[1].local, { state: 'incomplete', reason: '缺少产物位置' })
  assert.deepEqual(rows[2].local, { state: 'incomplete', reason: '缺少构建命令' })
  assert.deepEqual(rows[3].cloud, { state: 'incomplete', reason: '缺少云端构建流程' })
})

test('same display name cannot collapse independent versions, working directories, or OS variants', () => {
  for (const overrides of [{ versionGroup: 'other' }, { workingDir: 'other/app' }, { runner: { type: 'local', os: ['darwin'] } }]) {
    const rows = getReleaseSetupSummary(config([target('a', 'PC'), target('b', 'PC · 本地', 'local', overrides)]))
    assert.equal(rows.length, 2)
    assert.notEqual(rows[0].id, rows[1].id)
    assert.notEqual(rows[0].name, rows[1].name)
  }
})

test('an unspecified cloud OS cannot claim both independent Windows and macOS configurations', () => {
  const rows = getReleaseSetupSummary(config([
    target('cloud', 'PC · 云端', 'git-push', { runner: { type: 'git-push', os: [] } }),
    target('windows', 'PC · 本地'),
    target('macos', 'PC · 本地', 'local', { runner: { type: 'local', os: ['darwin'] } }),
  ]))
  assert.equal(rows.length, 3)
  assert.equal(rows.filter(row => row.cloud.state === 'configured').length, 1)
  assert.equal(rows.filter(row => row.local.state === 'configured').length, 2)
})

test('no configuration has no summary and a package-only local target remains valid', () => {
  assert.deepEqual(getReleaseSetupSummary(null), [])
  assert.deepEqual(getReleaseSetupSummary(config([])), [])
  const [row] = getReleaseSetupSummary(config([target('pack', '浏览器扩展', 'local', { steps: { package: 'npm run package' } })]))
  assert.equal(row.local.state, 'configured')
})

test('artifact rules independently configure final local outputs', () => {
  const rows = getReleaseSetupSummary(config([
    target('rules-only', '规则产物', 'local', { artifacts: [], artifactRules: [{ pattern: 'out/*.exe', min: 1, max: 1 }] }),
    target('blank-rule', '空规则', 'local', { artifacts: [], artifactRules: [{ pattern: ' ', min: 1, max: 1 }] }),
  ]))
  assert.equal(rows[0].local.state, 'configured')
  assert.deepEqual(rows[1].local, { state: 'incomplete', reason: '缺少产物位置' })
})

test('explicit cloud dispatch needs automation and an account while tag push does not', () => {
  const missingAutomation = { ...config([target('cloud', 'PC', 'git-push')]), automation: undefined }
  assert.deepEqual(getReleaseSetupSummary(missingAutomation)[0].cloud, { state: 'incomplete', reason: '缺少云端构建流程' })
  const missingAccount = { ...missingAutomation, automation: { trigger: 'dispatch', account: ' ' } }
  assert.deepEqual(getReleaseSetupSummary(missingAccount)[0].cloud, { state: 'incomplete', reason: '缺少云端构建账号' })
  const tagPush = { ...missingAutomation, targets: [target('cloud', 'PC', 'git-push', { steps: { publish: 'tag-push' } })] }
  assert.equal(getReleaseSetupSummary(tagPush)[0].cloud.state, 'configured')
})
