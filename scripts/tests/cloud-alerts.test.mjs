import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { ref, computed } from 'vue'
import { parse } from '@vue/compiler-sfc'

const source = parse(readFileSync(new URL('../../src/components/CloudBuildAlerts.vue', import.meta.url), 'utf8')).descriptor.scriptSetup.content
const ast = ts.createSourceFile('CloudBuildAlerts.ts', source, ts.ScriptTarget.Latest, true)
function fixture() {
  const alerts = ref([]), notices = ref([]), noticed = new Set(), events = [], saved = []
  const dependencies = { alerts, notices, noticed, notice: computed(() => notices.value[0]), emit: (...args) => events.push(args), sessionStorage: { setItem: (...args) => saved.push(args) }, getBaseURL: () => 'test' }
  const names = ['update', 'closeNotice', 'viewNotice']
  const compiled = ts.transpileModule(ast.statements.filter(s => names.includes(s.name?.text)).map(s => s.getText(ast)).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  const functions = new Function(...Object.keys(dependencies), `${compiled}; return {${names.join(',')}}`)(...Object.values(dependencies))
  return { ...dependencies, ...functions, events, saved }
}
const failure = { alertKey: 'run:42:1', appId: 'localplay', appName: 'localPlay', state: 'failed', version: 'web-server/v2.0.27' }

test('failure notifies once; dismissing notification keeps the card badge across repeated polls', () => {
  const f = fixture()
  f.update([failure]); f.update([failure])
  assert.equal(f.notices.value.length, 1)
  f.closeNotice(); f.update([failure])
  assert.equal(f.notices.value.length, 0)
  assert.equal(f.alerts.value.length, 1)
  assert.deepEqual(JSON.parse(f.saved[0][1]), [failure.alertKey])
  f.update([{ ...failure, alertKey: 'run:42:2' }])
  assert.equal(f.notices.value.length, 1, 'a new failed attempt needs a new notification')
})

test('details opens the failing project; acknowledged/recovered alerts clear without success popups', () => {
  const f = fixture()
  f.update([failure]); f.viewNotice()
  assert.deepEqual(f.events.at(-1), ['open', 'localplay'])
  assert.equal(f.alerts.value.length, 1)
  f.update([{ ...failure, alertKey: 'run:42:2' }]); f.update([])
  assert.equal(f.notices.value.length, 0)
  assert.equal(f.alerts.value.length, 0)
})

test('dismiss one notification without losing another project notification', () => {
  const f = fixture()
  f.update([failure, { ...failure, appId: 'other', alertKey: 'other:99:1' }])
  f.closeNotice()
  assert.equal(f.notices.value[0].appId, 'other')
  assert.deepEqual(JSON.parse(f.saved[0][1]), [failure.alertKey])
})
