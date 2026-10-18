import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = fileURLToPath(new URL('../../', import.meta.url))
const template = readFileSync(path.join(root, 'src-tauri/windows/installer.nsi'), 'utf8').replace(/\r\n/g, '\n')
const hook = path.join(root, 'src-tauri/windows/installer-hooks.nsh')
const hooks = readFileSync(hook, 'utf8')
const block = name => {
  const match = template.match(new RegExp('; RUNDOCK BEGIN ' + name + '\\n([\\s\\S]*?); RUNDOCK END ' + name + '\\n'))
  assert.ok(match, name)
  return match[1]
}
const makensis = process.env.RUNDOCK_MAKENSIS
const go = process.env.RUNDOCK_GO
const native = { skip: !makensis || !go ? 'Set RUNDOCK_MAKENSIS and RUNDOCK_GO for isolated native checks' : false, timeout: 120000 }
let stage, helper
function prepareFixture() {
  if (stage) return
  mkdirSync(path.join(root, '.tmp'), { recursive: true })
  stage = mkdtempSync(path.join(root, '.tmp/installer-hardening-native-'))
  helper = path.join(stage, 'helper.exe')
  execFileSync(go, ['-C', path.join(root, 'sidecar'), 'build', '-o', helper, './cmd/rundock-install-helper'], { windowsHide: true, timeout: 90000 })
}
function compile(name, source) {
  const executable = path.join(stage, name + '.exe')
  const script = path.join(stage, name + '.nsi')
  writeFileSync(script, `Unicode true\nRequestExecutionLevel user\n!include "MUI2.nsh"\n!include "FileFunc.nsh"\n!include "StrFunc.nsh"\n\${StrCase}\n\${StrLoc}\n!define RUNDOCK_INSTALL_HELPER "${helper}"\n!include "${hook}"\nName "RunDock isolated hardening fixture"\nOutFile "${executable}"\n!insertmacro MUI_LANGUAGE "English"\n!insertmacro MUI_LANGUAGE "SimpChinese"\n${source}`)
  execFileSync(makensis, ['/V2', '/INPUTCHARSET', 'UTF8', script], { windowsHide: true, timeout: 30000 })
  return executable
}
function run(executable, args = ['/S']) {
  const result = spawnSync(executable, args, { windowsHide: true, timeout: 30000 })
  assert.ifError(result.error)
  assert.equal(result.signal, null)
  return result.status
}

test('core extraction cannot be skipped and both file operations are checked', () => {
  assert.match(block('main-extraction-prepare'), /AllowSkipFiles off[\s\S]*ClearErrors/)
  assert.match(block('binary-extraction-prepare'), /ClearErrors/)
  assert.match(block('main-extraction-check'), /RunDockCheckExtraction/)
  assert.match(block('binary-extraction-check'), /RunDockCheckExtraction/)
  assert.match(hooks, /!macro RunDockCheckExtraction[\s\S]*Call RunDockRollbackInstall[\s\S]*SetErrorLevel 1[\s\S]*Abort/)
  assert.match(hooks, /Function \.onInstFailed[\s\S]*Call RunDockRollbackInstall/)
})

test('MSI migration guard covers silent init as well as the maintenance page', () => {
  assert.match(block('init-msi-guard'), /Call RunDockCheckUnattendedMsi/)
  for (const name of ['silent-msi-guard', 'msi-leave-guard']) {
    assert.match(block(name), /\$\{Silent\}/)
    assert.match(block(name), /\$UpdateMode = 1/)
    assert.match(block(name), /Abort/)
  }
  assert.ok(template.indexOf('; RUNDOCK BEGIN init-msi-guard') < template.indexOf('Section EarlyChecks'))
})

test('real NSIS commit rejects incomplete payload and extraction failure preserves recovery', native, () => {
  prepareFixture()
  const invalid = path.join(stage, 'invalid.exe')
  writeFileSync(invalid, 'not a PE executable')
  for (const scenario of ['success', 'invalid', 'extract-error', 'guard-error']) {
    const install = path.join(stage, scenario + ' 中文')
    mkdirSync(install)
    for (const name of ['launcher-platform.exe', 'launcher-sidecar.exe']) writeFileSync(path.join(install, name), 'original ' + name)
    const exe = compile(scenario, `InstallDir "${install}"
Section
SetOutPath $INSTDIR
!insertmacro NSIS_HOOK_PREINSTALL
${block('main-extraction-prepare')}
File /oname=launcher-platform.exe "${helper}"
${block('main-extraction-check')}
${scenario === 'extract-error' ? 'CreateDirectory "$INSTDIR\\launcher-sidecar.exe"' : ''}
${block('binary-extraction-prepare')}
File /oname=launcher-sidecar.exe "${scenario === 'invalid' ? invalid : helper}"
${scenario === 'guard-error' ? 'SetErrors' : ''}
${block('binary-extraction-check')}
!insertmacro NSIS_HOOK_POSTINSTALL
SectionEnd
`)
    assert.equal(run(exe) === 0, scenario === 'success', scenario)
    const journal = path.join(install, '.rundock-install-backup.json')
    if (scenario === 'extract-error') {
      assert.ok(existsSync(journal), 'failed rollback must preserve its journal')
      assert.equal(readdirSync(install).filter(name => name.includes('.rundock-running-')).length, 2)
      // Only the fixture's empty blocking directory is removed. A new owner
      // then recovers the dead NSIS transaction through the production helper.
      rmdirSync(path.join(install, 'launcher-sidecar.exe'))
      assert.equal(run(helper, ['prepare', install, String(process.pid)]), 0)
      assert.equal(run(helper, ['rollback', install, String(process.pid)]), 0)
    }
    assert.ok(!existsSync(journal), scenario)
    for (const name of ['launcher-platform.exe', 'launcher-sidecar.exe']) {
      const actual = readFileSync(path.join(install, name))
      if (scenario === 'success') assert.deepEqual(actual, readFileSync(helper))
      else assert.equal(actual.toString(), 'original ' + name)
    }
  }
})

test('real NSIS refuses /S, /UPDATE and passive MSI migration before staging', native, () => {
  prepareFixture()
  // Compile the actual guard against a unique HKCU test key. No real RunDock
  // uninstall key, installation directory or running application is touched.
  const key = 'Software\\RunDockInstallerTests\\' + randomUUID() + '\\Uninstall'
  const guarded = block('silent-msi-guard')
    .replaceAll('SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall', key)
    .replaceAll('RunDockCheckMsiHive HKLM', 'RunDockCheckMsiHive HKCU')
    .replaceAll('MessageBox MB_OK|MB_ICONSTOP "$(RunDockMsiMigrationBlocked)" /SD IDOK', 'DetailPrint "fixture migration blocked"')
  try {
  for (const [mode, args, installed] of [
    ['silent', ['/S'], true], ['update', ['/UPDATE'], true], ['passive', ['/P'], true], ['clean', ['/S'], false],
  ]) {
    const marker = path.join(stage, mode + '.marker')
    const exe = compile('msi-' + mode, `!define PRODUCTNAME "RunDock fixture"
!define MANUFACTURER "RunDock fixture publisher"
Var UpdateMode
Var PassiveMode
${guarded}
Function .onInit
  SetRegView 64
  DeleteRegKey HKCU "${key}"
  ${installed ? `WriteRegStr HKCU "${key}\\fixture" "DisplayName" "RunDock fixture"
  WriteRegStr HKCU "${key}\\fixture" "Publisher" "RunDock fixture publisher"
  WriteRegDWORD HKCU "${key}\\fixture" "WindowsInstaller" 1
  WriteRegStr HKCU "${key}\\fixture" "UninstallString" "MsiExec.exe /X fixture"` : ''}
  \${GetOptions} $CMDLINE "/UPDATE" $UpdateMode
  \${IfNot} \${Errors}
    StrCpy $UpdateMode 1
  \${EndIf}
  \${GetOptions} $CMDLINE "/P" $PassiveMode
  \${IfNot} \${Errors}
    StrCpy $PassiveMode 1
  \${EndIf}
  Call RunDockCheckUnattendedMsi
FunctionEnd
Function .onGUIEnd
  DeleteRegKey HKCU "${key}"
FunctionEnd
Section
  FileOpen $0 "${marker}" w
  FileWrite $0 "install section reached"
  FileClose $0
SectionEnd
`)
    assert.equal(run(exe, args) === 0, !installed, mode)
    assert.equal(existsSync(marker), !installed, mode)
  }
  } finally {
    // .onGUIEnd is not guaranteed after .onInit aborts. Remove exactly this
    // fixture key even when an assertion fails; no real uninstall keys exist here.
    spawnSync(path.join(process.env.SystemRoot, 'System32/reg.exe'), ['delete', 'HKCU\\' + key, '/f', '/reg:64'], { windowsHide: true })
  }
})
