import assert from 'node:assert/strict'
import test from 'node:test'
import { build } from 'esbuild'

const result = await build({ entryPoints: ['src/utils/releasePresentation.ts'], bundle: true, write: false, format: 'esm', platform: 'node' })
const { releaseTargetLabel, isAlternateBuildTarget } = await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString('base64')}`)
const target = (id, name, runner, versionGroup = 'extension', kind = 'custom') => ({ id, name, kind, versionGroup, runner: { type: runner } })

test('remove only mode suffixes, preserving product names', () => {
  assert.equal(releaseTargetLabel('浏览器扩展 · 云端'), '浏览器扩展')
  assert.equal(releaseTargetLabel('浏览器扩展 · 本地'), '浏览器扩展')
  assert.equal(releaseTargetLabel('Android · 云端'), 'Android')
  assert.equal(releaseTargetLabel('Web + 服务器后端'), 'Web + 服务器后端')
  assert.equal(releaseTargetLabel('云端资料 · 专业版'), '云端资料 · 专业版')
})

test('only same product/version group with different runners is an alternate', () => {
  const cloud = target('cloud', '浏览器扩展 · 云端', 'git-push')
  assert.equal(isAlternateBuildTarget(cloud, target('local', '浏览器扩展 · 本地', 'local')), true)
  assert.equal(isAlternateBuildTarget(cloud, target('local', '浏览器扩展 · 本地', 'local', 'another')), false)
  assert.equal(isAlternateBuildTarget(cloud, target('local', '另一款插件 · 本地', 'local')), false)
  assert.equal(isAlternateBuildTarget(cloud, target('other', '浏览器扩展 · 云端', 'git-push')), false)
  assert.equal(isAlternateBuildTarget(cloud, cloud), false)
})
