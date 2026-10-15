import assert from 'node:assert/strict'
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { createServer, loadConfigFromFile } from 'vite'

const configFile = fileURLToPath(new URL('../../vite.config.ts', import.meta.url))
const cacheFolders = [
  'src-tauri/target', 'src-tauri/gen', '.gocache', '.tmp',
  'sidecar/.tmp', '.launcher', 'outputs', 'reports',
]

test('development dependency discovery is limited to the application entry', async () => {
  const loaded = await loadConfigFromFile({ command: 'serve', mode: 'development' }, configFile)
  assert.deepEqual(loaded.config.optimizeDeps.entries, ['index.html'])
})

test('development watches source changes without traversing generated caches', { timeout: 15000 }, async () => {
  // Keep fixture changes outside the running application's watch tree.
  const root = await mkdtemp(path.join(tmpdir(), 'rundock-watch-test-'))
  let server
  let watcherReady
  let readyTimeout
  try {
    await mkdir(path.join(root, 'src'))
    await writeFile(path.join(root, 'index.html'), '<script type="module" src="/src/main.js"></script>')
    const source = path.join(root, 'src/main.js')
    await writeFile(source, 'export const marker = "before"')
    for (const folder of cacheFolders) {
      await mkdir(path.join(root, folder, 'nested'), { recursive: true })
      await writeFile(path.join(root, folder, 'nested/generated.js'), 'export default 1')
    }
    server = await createServer({
      root, configFile, cacheDir: path.join(root, 'node_modules/.vite'),
      server: { middlewareMode: true, hmr: false },
      plugins: [{
        name: 'observe-test-watcher',
        configureServer(instance) {
          // Middleware-mode creation can finish after the ready event fires.
          watcherReady = new Promise((resolve, reject) => {
            readyTimeout = setTimeout(() => reject(new Error('File watcher did not become ready')), 5000)
            instance.watcher.once('ready', () => { clearTimeout(readyTimeout); resolve() })
          })
        },
      }],
    })
    await watcherReady
    // Vite can add config dependencies while Chokidar is still adding root
    // descendants; observe the source directory rather than the first ready.
    const deadline = Date.now() + 5000
    while (!Object.keys(server.watcher.getWatched()).some(dir => path.resolve(dir) === path.join(root, 'src'))) {
      if (Date.now() >= deadline) break
      await new Promise(resolve => setTimeout(resolve, 25))
    }
    const normalize = dir => {
      const value = dir.replaceAll('\\', '/')
      return process.platform === 'win32' ? value.toLowerCase() : value
    }
    const watched = Object.keys(server.watcher.getWatched()).map(normalize)
    const normalizedRoot = normalize(root)
    assert.ok(watched.includes(`${normalizedRoot}/src`), `Source HMR must stay enabled: ${JSON.stringify(watched)}`)
    for (const folder of cacheFolders) {
      const prefix = `${normalizedRoot}/${folder}`
      assert.ok(!watched.some(dir => dir === prefix || dir.startsWith(`${prefix}/`)), `Cache was watched: ${folder}`)
    }
    assert.match((await server.transformRequest('/src/main.js')).code, /before/)
    const changed = new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('Source change was not observed')), 5000)
      const onChange = file => {
        if (path.resolve(file) !== source) return
        clearTimeout(timeout)
        server.watcher.off('change', onChange)
        resolve()
      }
      server.watcher.on('change', onChange)
    })
    await writeFile(source, 'export const marker = "after"')
    await changed
    assert.match((await server.transformRequest('/src/main.js')).code, /after/)
  } finally {
    clearTimeout(readyTimeout)
    await server?.close()
    await rm(root, { recursive: true, force: true })
  }
})
