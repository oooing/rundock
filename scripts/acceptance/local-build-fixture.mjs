// Real fixture build: generate a runnable Node bundle and gzip it; no remote calls.
import { mkdirSync, writeFileSync, readFileSync, readdirSync, lstatSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import path from 'node:path'

export function makeFixture(base, name, git = false) {
  const root = path.join(base, name)
  mkdirSync(path.join(root, '.launcher'), { recursive: true })
  writeFileSync(path.join(root, 'start.cmd'), '@echo off\r\nexit /b 0\r\n')
  writeFileSync(path.join(root, 'package.json'), JSON.stringify({ name, version: '1.2.3' }, null, 2))
  writeFileSync(path.join(root, 'source.cjs'), 'module.exports = "original fixture app";\n')
  const builder = `const fs=require('node:fs'),z=require('node:zlib'),crypto=require('node:crypto');
const [phase,mode,version]=process.argv.slice(2);const actual=JSON.parse(fs.readFileSync('package.json')).version;
if(version!==actual)throw Error('version mismatch');if(mode==='fail')throw Error('fixture compiler failed');
if(mode==='slow'&&phase==='build'){setTimeout(()=>process.exit(0),20000);return;}
if(phase==='check'){console.log('source check passed');return;}
if(mode==='noisy'&&phase==='build'){for(let i=0;i<650;i++)console.log('compiler progress '+i);throw Error('noisy-tail-marker: compiler failed at the end');}
fs.mkdirSync('out',{recursive:true});
if(phase==='build'){fs.writeFileSync('out/app.cjs',fs.readFileSync('source.cjs'));console.log('bundle built');return;}
if(phase==='package'){
 if(mode==='missing')return;
 const body=fs.existsSync('out/app.cjs')?fs.readFileSync('out/app.cjs'):fs.readFileSync('source.cjs');const file='out/'+(mode==='long'?'long-package-'.repeat(10):'app-')+version+'.cjs.gz';fs.writeFileSync(file,z.gzipSync(body));
 fs.writeFileSync(file+'.sha256',crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')+'\\n');return;
}
if(phase==='verify'){if(mode==='badverify')throw Error('package verification failed');
 if(!z.gunzipSync(fs.readFileSync('out/'+(mode==='long'?'long-package-'.repeat(10):'app-')+version+'.cjs.gz')).equals(fs.readFileSync('source.cjs')))throw Error('wrong package');
 console.log('package verified');return;
}
throw Error('unexpected phase');
`
  writeFileSync(path.join(root, 'builder.cjs'), builder)
  const command = (phase, mode = 'good') => `"${process.execPath}" builder.cjs ${phase} ${mode} \${VERSION}`
  const target = (id, mode, overrides = {}) => ({
    id, name: `Fixture ${id}`, kind: 'desktop', versionGroup: 'app', workingDir: '.', enabled: true,
    confidence: 1, runner: { type: 'local', os: ['windows', 'linux', 'darwin'] },
    steps: { check: command('check', mode), build: command('build', mode), package: command('package', mode),
      publish: 'echo FORBIDDEN_UPLOAD > uploaded.txt', deploy: 'echo FORBIDDEN_DEPLOY > deployed.txt' },
    artifacts: ['out/**'], artifactRules: [
      { pattern: 'out/app-${VERSION}.cjs.gz', min: 1, max: 1 },
      { pattern: 'out/app-${VERSION}.cjs.gz.sha256', min: 1, max: 1 },
    ], verification: [{ name: 'package integrity', command: command('verify', mode), timeoutSeconds: 10 }],
    timeouts: { check: 10, build: 30, package: 10 }, ...overrides,
  })
  const longName = 'long-package-'.repeat(10)
  const config = { schemaVersion: 1, versionGroups: [{ id: 'app', name: 'App', currentVersion: '0.0.1',
    versionFiles: [{ path: 'package.json', format: 'json', jsonPointer: '/version' }] }], targets: [
    target('good', 'good'), target('fail', 'fail'), target('missing', 'missing'), target('slow', 'slow'),
    target('badverify', 'badverify'), target('noisy', 'noisy'), target('empty', 'good', { steps: {} }),
    target('package-only', 'good', { steps: { package: command('package') } }),
    target('long-target-'.repeat(5) + 'packages', 'long', { artifactRules: [
      { pattern: `out/${longName}\${VERSION}.cjs.gz`, min: 1, max: 1 },
      { pattern: `out/${longName}\${VERSION}.cjs.gz.sha256`, min: 1, max: 1 },
    ] }),
    target('builtin', 'good', { steps: { check: command('check'), build: command('build'), package: command('package') },
      delivery: { provider: 'github', repository: 'fixture/project', account: 'fixture', workflowPolicy: 'dispatch-only', makeLatest: false } }),
    target('cloud', 'good', { runner: { type: 'git-push', os: [] }, steps: { publish: 'tag-push' } }),
  ] }
  writeFileSync(path.join(root, '.launcher/release.yaml'), JSON.stringify(config, null, 2))
  writeFileSync(path.join(root, '.gitignore'), 'out/\nnode_modules/\n')
  if (git) {
    gitAt(root, 'init', '-b', 'main')
    gitAt(root, 'config', 'core.autocrlf', 'false')
    gitAt(root, 'config', 'user.name', 'Local build acceptance')
    gitAt(root, 'config', 'user.email', 'acceptance@example.invalid')
    gitAt(root, 'add', '.')
    gitAt(root, 'commit', '-m', 'fixture baseline')
    gitAt(root, 'tag', 'v1.2.3')
  }
  return { root, config }
}

export function gitAt(root, ...args) {
  return execFileSync('git', ['-C', root, ...args], { encoding: 'utf8', windowsHide: true,
    env: { ...process.env, GIT_TERMINAL_PROMPT: '0' } }).trim()
}

export function fingerprint(root, git = false) {
  const files = {}
  const walk = folder => {
    for (const name of readdirSync(folder).sort()) {
      if (name === '.git') continue
      const file = path.join(folder, name), info = lstatSync(file)
      if (info.isDirectory()) walk(file)
      else files[path.relative(root, file)] = createHash('sha256').update(readFileSync(file)).digest('hex')
    }
  }
  walk(root)
  return { files, ...(git ? { head: gitAt(root, 'rev-parse', 'HEAD'), index: gitAt(root, 'ls-files', '--stage'),
    status: gitAt(root, 'status', '--porcelain=v1', '--untracked-files=all'), tags: gitAt(root, 'show-ref', '--tags') } : {}) }
}
