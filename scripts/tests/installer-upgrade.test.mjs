import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = fileURLToPath(new URL('../../', import.meta.url))
const template = readFileSync(path.join(root, 'src-tauri/windows/installer.nsi'), 'utf8').replace(/\r\n/g, '\n')
const block = (name) => {
  const match = template.match(new RegExp('; RUNDOCK BEGIN ' + name + '\\n([\\s\\S]*?); RUNDOCK END ' + name + '\\n'))
  assert.ok(match, name)
  return match[1]
}

test('custom installer is the pinned official template plus scoped upgrade policy', () => {
  const config = JSON.parse(readFileSync(path.join(root, 'src-tauri/tauri.conf.json'), 'utf8'))
  assert.equal(config.bundle.windows.nsis.template, 'windows/installer.nsi')
  const upstream = template.replace(/; RUNDOCK BEGIN ([\w-]+)\n[\s\S]*?; RUNDOCK END \1\n/g, '')
  const blob = Buffer.from(upstream)
  const hash = createHash('sha1').update('blob ' + blob.length + '\0').update(blob).digest('hex')
  // Upgrade this pin and review/rebase our blocks together when updating Tauri CLI.
  assert.equal(hash, 'd372e3c391770cf231db974422a1e4f8adaac3a6')
  const lock = JSON.parse(readFileSync(path.join(root, 'package-lock.json'), 'utf8'))
  assert.equal(lock.packages['node_modules/@tauri-apps/cli'].version, '2.11.4')
})

test('in-place install bypasses uninstall, without changing runtime repair, data or migration', () => {
  const policy = block('policy')
  assert.doesNotMatch(policy, /StrCpy \$UpdateMode|Exec|Delete|RMDir|WriteReg/)
  assert.match(block('skip-maintenance'), /Call RunDockChooseInPlaceUpgrade[\s\S]*?Abort/)
  assert.match(block('leave-guard'), /Goto reinst_done/)
  const page = template.slice(template.indexOf('Function PageReinstall\n'), template.indexOf('Function PageReinstallUpdateSelection'))
  assert.ok(page.indexOf('SemverCompare') < page.indexOf('Call RunDockChooseInPlaceUpgrade'))
  assert.ok(page.indexOf('Call RunDockChooseInPlaceUpgrade') < page.indexOf('nsDialogs::Create'))
  const leave = template.slice(template.indexOf('Function PageLeaveReinstall\n'))
  assert.ok(leave.indexOf('Goto reinst_uninstall') < leave.indexOf('; RUNDOCK BEGIN leave-guard'), 'MSI migration stays explicit')
})

test('real NSIS policy handles upgrade, reinstall, downgrade, unknown versions and MSI migration', {
  skip: !process.env.RUNDOCK_MAKENSIS ? 'Set RUNDOCK_MAKENSIS for isolated native policy execution' : false,
}, () => {
  // Executes ONLY the extracted decision function, never a real installer,
  // uninstall, registry mutation, process shutdown, or user-data operation.
  mkdirSync(path.join(root, '.tmp'), { recursive: true })
  const stage = mkdtempSync(path.join(root, '.tmp/installer-upgrade-'))
  const source = path.join(stage, 'policy.nsi')
  const output = path.join(stage, 'result.txt')
  const exe = path.join(stage, 'policy.exe')
  const cases = [
    ['1', '0', 1], ['0', '0', 1], ['-1', '0', 0],
    ['error', '0', 0], ['', '0', 0],
    ['1', '1', 0], ['0', '1', 0], ['-1', '1', 0],
    ['1', '', 1], ['0', '', 1],
  ]
  const steps = cases.map(([compare, wix], index) =>
    'StrCpy $R0 "' + compare + '"\nStrCpy $WixMode "' + wix + '"\n'
    + 'Call RunDockChooseInPlaceUpgrade\n'
    + 'FileWrite $0 "' + index + ':$RunDockInPlaceUpgrade:$UpdateMode$\\r$\\n"\n').join('')
  writeFileSync(source, 'Unicode true\nRequestExecutionLevel user\nSilentInstall silent\n'
    + '!include "LogicLib.nsh"\nName "RunDock isolated upgrade policy test"\n'
    + 'OutFile "' + exe + '"\nVar WixMode\nVar UpdateMode\n' + block('policy')
    + 'Section\nStrCpy $UpdateMode 0\nFileOpen $0 "' + output + '" w\n'
    + steps + 'FileClose $0\nSectionEnd\n')
  execFileSync(process.env.RUNDOCK_MAKENSIS, ['/V2', '/INPUTCHARSET', 'UTF8', source], { windowsHide: true })
  execFileSync(exe, ['/S'], { windowsHide: true, timeout: 15000 })
  const result = readFileSync(output, 'utf8').trim().split(/\r?\n/)
  assert.deepEqual(result, cases.map(([, , expected], index) => index + ':' + expected + ':0'))
})
