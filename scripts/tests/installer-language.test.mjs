import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = fileURLToPath(new URL('../../', import.meta.url))
const config = JSON.parse(readFileSync(path.join(root, 'src-tauri/tauri.conf.json'), 'utf8'))
const hook = path.join(root, 'src-tauri/windows/installer-hooks.nsh')

test('EXE uses system display language, Chinese primary match, English fallback, no selector', () => {
  assert.deepEqual(config.bundle.windows.nsis.languages, ['English', 'SimpChinese'])
  assert.equal(config.bundle.windows.nsis.displayLanguageSelector, false)
  // Do not change application identity or MSI upgrade identity while localizing.
  assert.equal(config.identifier, 'com.launcher.platform')
  assert.equal(config.bundle.windows.wix.upgradeCode, '3dab43c3-d2d7-5eba-adb3-07bfe05728bb')
})

const makensis = process.env.RUNDOCK_MAKENSIS
test('real NSIS language tables and custom messages match the app language policy', {
  skip: !makensis ? 'Set RUNDOCK_MAKENSIS to run the isolated Windows compiler/runtime check' : false,
}, () => {
  assert.equal(process.platform, 'win32')
  assert.ok(existsSync(makensis))
  mkdirSync(path.join(root, '.tmp'), { recursive: true })
  const dir = mkdtempSync(path.join(root, '.tmp/installer-language-'))
  const output = path.join(dir, 'result.txt')
  const source = path.join(dir, 'language-test.nsi')
  const executable = path.join(dir, 'language-test.exe')
  // This is NOT a RunDock installer. No registry writes, installation, process
  // shutdown, or uninstall hooks are executed; only language tables are read.
  writeFileSync(source, `Unicode true
RequestExecutionLevel user
SilentInstall silent
!include "MUI2.nsh"
!include "FileFunc.nsh"
!include "${hook}"
Name "RunDock isolated language test"
OutFile "${executable}"
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"
Function .onInit
  \${GetParameters} $R0
  ClearErrors
  \${GetOptions} $R0 "/TESTLANG=" $R1
  \${IfNot} \${Errors}
    StrCpy $LANGUAGE $R1
  \${EndIf}
FunctionEnd
Section
  FileOpen $0 "${output}" w
  FileWriteUTF16LE $0 "$LANGUAGE$\\r$\\n$(RunDockClosing)$\\r$\\n$(RunDockCloseFailed)$\\r$\\n$(RunDockLegacyMigration)"
  FileClose $0
SectionEnd
`, 'utf8')
  execFileSync(makensis, ['/V2', '/INPUTCHARSET', 'UTF8', source], { windowsHide: true })
  // NSIS resolves exact match, then primary language, then the first language.
  for (const [language, expected] of [
    [2052, 2052], [1028, 2052], [3076, 2052], [4100, 2052], [5124, 2052],
    [1033, 1033], [2057, 1033], [1036, 1033], [1041, 1033], [1031, 1033],
  ]) {
    execFileSync(executable, ['/S', `/TESTLANG=${language}`], { windowsHide: true, timeout: 10000 })
    const text = readFileSync(output, 'utf16le')
    assert.equal(Number(text.split('\r\n')[0]), expected, `LANGID ${language}`)
    assert.ok(text.includes(expected === 2052 ? '正在关闭 RunDock' : 'Closing RunDock'))
    assert.ok(text.includes(expected === 2052 ? '项目数据未被删除' : 'Your project data has not been removed'))
    assert.ok(text.includes(expected === 2052 ? 'Launcher 已更名为 RunDock' : 'Launcher is now RunDock'))
  }
})
