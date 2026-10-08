import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const root = fileURLToPath(new URL('../../', import.meta.url))

test('current Tauri bundler compiles bilingual hooks and supports the updater handoff', {
  skip: process.env.RUNDOCK_TEST_BUNDLE !== '1' ? 'Opt in with RUNDOCK_TEST_BUNDLE=1 (Rust and NSIS required)' : false,
  timeout: 120000,
}, () => {
  assert.equal(process.platform, 'win32')
  mkdirSync(path.join(root, '.tmp'), { recursive: true })
  const stage = mkdtempSync(path.join(root, '.tmp/installer-bundle-'))
  const native = path.join(stage, 'src-tauri')
  for (const folder of ['src', 'windows', 'target/release']) mkdirSync(path.join(native, folder), { recursive: true })
  mkdirSync(path.join(stage, 'dist'))
  writeFileSync(path.join(stage, 'dist/index.html'), '<!doctype html><title>Installer compile-only test</title>')
  writeFileSync(path.join(stage, 'package.json'), '{"name":"rundock-installer-test","version":"0.0.0"}')
  writeFileSync(path.join(native, 'Cargo.toml'), '[package]\nname="rundock-installer-test"\nversion="0.0.0"\nedition="2021"\n[workspace]\n')
  const main = path.join(native, 'src/main.rs')
  writeFileSync(main, 'fn main() {}')
  const config = JSON.parse(readFileSync(path.join(root, 'src-tauri/tauri.conf.json'), 'utf8'))
  // Compile only. Never execute this setup. Even the embedded app is a no-op,
  // and the bundle has a separate identity so it cannot replace real RunDock.
  const fixtureConfig = {
    productName: 'RunDock Installer Test', version: '0.0.0', identifier: 'com.rundock.installer-test',
    build: { frontendDist: '../dist' }, app: { windows: [] },
    bundle: { active: true, targets: ['nsis'], icon: [path.join(root, 'src-tauri/icons/icon.ico')],
      windows: { nsis: config.bundle.windows.nsis, webviewInstallMode: { type: 'skip' } } },
  }
  writeFileSync(path.join(native, 'tauri.conf.json'), JSON.stringify(fixtureConfig))
  for (const file of ['installer-hooks.nsh', 'stop-installed-app.ps1']) {
    copyFileSync(path.join(root, 'src-tauri/windows', file), path.join(native, 'windows', file))
  }
  execFileSync('rustc', [main, '-o', path.join(native, 'target/release/rundock-installer-test.exe')], { windowsHide: true })
  execFileSync(process.execPath, [path.join(root, 'node_modules/@tauri-apps/cli/tauri.js'), 'bundle', '--bundles', 'nsis', '--ci'], {
    cwd: stage, windowsHide: true, timeout: 110000, stdio: 'pipe',
  })
  const template = readFileSync(path.join(native, 'target/release/nsis/x64/installer.nsi'), 'utf8')
  assert.match(template, /MUI_LANGUAGE "English"/)
  assert.match(template, /MUI_LANGUAGE "SimpChinese"/)
  assert.match(template, /DISPLAYLANGUAGESELECTOR "false"/)
  assert.match(template, /GetOptions[^\n]*"\/UPDATE"/)
  assert.match(template, /GetOptions[^\n]*"\/P"/)
  assert.match(template, /GetOptions[^\n]*"\/R"/)
  assert.match(template, /\$UpdateMode = 1[\s\S]*?Goto reinst_done/)
  assert.match(template, /\$PassiveMode = 1[\s\S]*?Call PageLeaveReinstall/)
  console.log(`Compile-only installer evidence: ${stage}`)
})
