import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
const read = path => readFileSync(new URL('../../' + path, import.meta.url), 'utf8');

test('development ports agree across the script, Vite, and desktop shell', () => {
  assert.match(read('scripts/dev.ps1'), /\$backendPort = 17655/);
  assert.match(read('scripts/dev.ps1'), /\$frontendPort = 1421/);
  assert.match(read('vite.config.ts'), /port: 1421/);
  assert.equal(JSON.parse(read('src-tauri/tauri.conf.json')).build.devUrl, 'http://127.0.0.1:1421');
  assert.match(read('src/api/base.ts'), /import.meta.env.DEV \? 'http:\/\/127.0.0.1:17655' : 'http:\/\/127.0.0.1:17654'/);
});

test('development entrypoint fails without pause and never overwrites the protected binary', () => {
  assert.doesNotMatch(read('scripts/dev.bat'), /\bpause\b/i);
  assert.match(read('scripts/dev.bat'), /exit \/b %errorlevel%/i);
  const script = read('scripts/dev.ps1');
  assert.doesNotMatch(script, /launcher-sidecar-dev\.exe/);
  assert.match(script, /launcher-sidecar-v2-dev\.exe/);
  assert.match(script, /launcher-sidecar-dev/);
  assert.ok(script.indexOf('Assert-PortFree $backendPort') < script.indexOf('& $go build'));
  assert.match(script, /LAUNCHER_DATA_DIR = \$DataDir/);
});
