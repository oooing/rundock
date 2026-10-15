import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
const source = readFileSync(new URL('../../src/utils/releaseSubmissionFeedback.ts', import.meta.url), 'utf8')
const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
const exports = {}; new Function('exports', code)(exports)
const feedback = exports.releaseSubmissionFeedback
const ready = { busy:false, error:'', notice:'', needsAttention:false, stale:false, ready:true, status:'passed', formal:true }

test('completed review clearly says that publication still needs confirmation', () => {
  assert.deepEqual(feedback(ready), {tone:'ready',text:'内容已就绪，尚未发布。请点击“确认发布”。'})
  assert.match(feedback({...ready,formal:false}).text,/尚未提交/)
  assert.equal(feedback({...ready,busy:true}),null)
  assert.equal(feedback({...ready,ready:false,status:undefined}),null)
})
test('errors, changed plans, stale checks, cancellation and findings cannot appear ready', () => {
  assert.deepEqual(feedback({...ready,error:'网络连接中断'}),{tone:'error',text:'网络连接中断'})
  assert.equal(feedback({...ready,notice:'请核对版本'}).text,'请核对版本')
  assert.match(feedback({...ready,stale:true}).text,/失效/)
  assert.match(feedback({...ready,status:'cancelled'}).text,/已取消/)
  assert.match(feedback({...ready,needsAttention:true}).text,/处理/)
})
