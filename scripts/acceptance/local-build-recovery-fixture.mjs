// Storage-fault fixture only: source/app runtime databases are never opened here.
import assert from 'node:assert/strict'
import path from 'node:path'
import { DatabaseSync } from 'node:sqlite'

function openFixtureDatabase(dir) {
  const resolved = path.resolve(dir)
  assert.match(resolved.replaceAll('\\', '/'), /\/sidecar\/\.tmp\/local-build-\d+$/)
  return new DatabaseSync(path.join(resolved, 'data/launcher.db'))
}

export function removeSealRegistration(dir, runId, expectedIntent = 'build-only') {
  const db = openFixtureDatabase(dir)
  try {
    const run = db.prepare('SELECT execution_plan_json FROM release_runs WHERE id=?').get(runId)
    assert.equal(JSON.parse(run.execution_plan_json).intent, expectedIntent)
    assert.ok(db.prepare('SELECT COUNT(*) AS count FROM release_artifacts WHERE release_run_id=?').get(runId).count > 0)
    assert.ok(db.prepare('DELETE FROM local_build_artifacts WHERE release_run_id=?').run(runId).changes > 0)
    db.prepare("UPDATE release_runs SET status='running',stage='local_saving',finished_at=NULL WHERE id=?").run(runId)
  } finally { db.close() }
}

export function recordedSnapshot(dir, runId) {
  const db = openFixtureDatabase(dir)
  try {
    const row = db.prepare('SELECT execution_plan_json FROM release_runs WHERE id=?').get(runId)
    return JSON.parse(row.execution_plan_json).snapshotRoot
  } finally { db.close() }
}

// Deterministic long-history diagnostic fixture. Keep the actual compiler
// failure at the end; exercise persisted row paging, not just one multiline log.
export function prependDiagnosticHistory(dir, runId) {
  const db = openFixtureDatabase(dir)
  let transaction = false
  try {
    const run = db.prepare('SELECT status,execution_plan_json FROM release_runs WHERE id=?').get(runId)
    assert.equal(run.status, 'failed')
    assert.equal(JSON.parse(run.execution_plan_json).intent, 'build-only')
    const logs = db.prepare('SELECT ts,stream,text FROM release_logs WHERE release_run_id=? ORDER BY id').all(runId)
    assert.ok(logs.some(line => line.text.includes('noisy-tail-marker')))
    db.exec('BEGIN IMMEDIATE')
    transaction = true
    db.prepare('DELETE FROM release_logs WHERE release_run_id=?').run(runId)
    const insert = db.prepare('INSERT INTO release_logs(release_run_id,stream,text) VALUES(?,?,?)')
    for (let line = 0; line < 650; line++) insert.run(runId, 'stdout', `Injected historical compiler progress ${line}`)
    const original = db.prepare('INSERT INTO release_logs(release_run_id,ts,stream,text) VALUES(?,?,?,?)')
    for (const line of logs) original.run(runId, line.ts, line.stream, line.text)
    db.exec('COMMIT')
    transaction = false
  } catch (error) {
    if (transaction) db.exec('ROLLBACK')
    throw error
  } finally { db.close() }
}
