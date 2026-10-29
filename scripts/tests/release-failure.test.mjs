import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const source = readFileSync(new URL('../../src/utils/releaseFailure.ts', import.meta.url), 'utf8')
const exports = {}
new Function('exports', ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText)(exports)
const { releaseFailureAdvice } = exports
const failed = { status: 'failed', stage: 'target_build', errorCode: 'target_step_failed' }
const event = text => ({ stream: 'event', text })
const stdout = text => ({ stream: 'stdout', text })
const disk = stdout('PC 客户端 · 本地\nerror: failed to write to `C:\\Users\\private-user\\Temp\\rd-native-build\\full.rmeta`: 磁盘空间不足 (os error 112)\nprivate-token-value')

test('legacy compile error shows a safe drive-specific reason and remedy', () => {
  const advice = releaseFailureAdvice(failed, [event('PC 客户端 · 本地：正在构建'), disk])
  assert.deepEqual(advice.values, ['C'])
  assert.match(advice.reason, /空间不足/)
  assert.match(advice.action, /临时目录.*重新构建/)
  assert.doesNotMatch(JSON.stringify(advice), /private-user|private-token|full.rmeta/)
})

test('uses the failing path drive, without assuming the project or system drive', () => {
  const advice = releaseFailureAdvice(failed, [stdout('error: failed to write to D:\\Builds\\metadata: no space left on device')])
  assert.deepEqual(advice.values, ['D'])
  const unknown = releaseFailureAdvice(failed, [stdout('rustc-LLVM ERROR: IO failure on output stream: no space on device')])
  assert.deepEqual(unknown.values, [])
  assert.match(unknown.reason, /构建磁盘/)
})

test('new disk error codes are readable even without available historical logs', () => {
  assert.ok(releaseFailureAdvice({ ...failed, errorCode: 'build_disk_full' }))
  assert.equal(releaseFailureAdvice(failed), null)
})

test('never misattributes old disk errors to another retry, target or phase', () => {
  for (const latest of ['重试：正在构建所选发布目标', 'Android：正在构建']) {
    assert.equal(releaseFailureAdvice(failed, [disk, event(latest), stdout('error: missing SDK')]), null)
    assert.equal(releaseFailureAdvice(failed, [disk, event(latest)]), null)
  }
  assert.equal(releaseFailureAdvice({ ...failed, errorCode: 'build_changed_tree' }, [disk]), null)
  assert.equal(releaseFailureAdvice({ ...failed, status: 'running' }, [disk]), null)
  assert.equal(releaseFailureAdvice({ ...failed, status: 'succeeded' }, [disk]), null)
  assert.equal(releaseFailureAdvice({ ...failed, stage: 'pushing_branch' }, [disk]), null)
  assert.equal(releaseFailureAdvice(failed, [stdout('some unrelated command failed')]), null)
})

test('all failure advice is translated', () => {
  const dict = JSON.parse(readFileSync(new URL('../../src/i18n/en.json', import.meta.url), 'utf8'))
  const advice = releaseFailureAdvice(failed, [disk])
  assert.ok(dict[advice.reason]); assert.ok(dict[advice.action])
  assert.ok(dict[releaseFailureAdvice({ ...failed, errorCode: 'build_disk_full' }).reason])
})

test('native failure markers explain old task logs without attributing a previous attempt', () => {
  const dict = JSON.parse(readFileSync(new URL('../../src/i18n/en.json', import.meta.url), 'utf8'))
  for (const kind of ['SOURCE', 'MODULE']) {
    const output = stdout(`BUILD SUCCESSFUL\n[ANDROID_NATIVE_${kind}_MISSING] private-path`)
    const advice = releaseFailureAdvice(failed, [output, event('已清理本次临时副本')])
    assert.ok(advice)
    assert.match(advice.action, /返回检查/)
    assert.ok(dict[advice.reason]); assert.ok(dict[advice.action])
    assert.doesNotMatch(JSON.stringify(advice), /private-path/)
    assert.equal(releaseFailureAdvice(failed, [output, event('Android：正在构建'), stdout('different failure')]), null)
  }
})
