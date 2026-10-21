// Local-only release snapshot. No source version changes and no remote writes.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import { execFileSync, spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
const root = process.cwd(), version = process.argv[2] || '2.0.48'
assert.match(version, /^\d+\.\d+\.\d+$/)
const evidence = path.join(root, 'outputs/acceptance', `silent-update-build-${Date.now()}`)
const snapshotRoot = path.join(root, '.tmp')
fs.mkdirSync(snapshotRoot, {recursive:true})
const work = fs.mkdtempSync(path.join(snapshotRoot, 'rd-update-'))
const project = path.join(work, 'code')
fs.mkdirSync(project); fs.mkdirSync(evidence, {recursive:true})
const env = {...process.env, GOPATH: path.join(os.homedir(),'gopath'), CARGO_BUILD_JOBS:'2', GH_TOKEN:'',GITHUB_TOKEN:'',GIT_TERMINAL_PROMPT:'0'}
const run=(exe,args,cwd=root)=>execFileSync(exe,args,{cwd,env,encoding:'utf8',windowsHide:true,maxBuffer:16e6,timeout:180000})
const digest=b=>createHash('sha256').update(b).digest('hex')
const files=run('git',['ls-files','-z']).split('\0').filter(Boolean)
const extras=run('git',['ls-files','--others','--exclude-standard','-z']).split('\0').filter(Boolean)
assert.ok(extras.every(f=> /^(src-tauri\/|sidecar\/|scripts\/acceptance\/)/.test(f) || ['scripts/tests/installer-hardening.test.mjs', 'docs/adr/2026-10-18-silent-install-explicit-activation.md'].includes(f)), 'Review untracked inputs before copying')
const source={}
for(const file of [...files,...extras]) {
 const from=path.join(root,file);if(!fs.existsSync(from))continue
 const bytes=fs.readFileSync(from);source[file]=digest(bytes)
 const to=path.join(project,file);fs.mkdirSync(path.dirname(to),{recursive:true});fs.writeFileSync(to,bytes)
}
for(const file of ['package.json','package-lock.json','src-tauri/tauri.conf.json']) {
 const target=path.join(project,file),raw=fs.readFileSync(target,'utf8'),old=JSON.parse(raw).version
 let next=raw.replace(`"version": "${old}"`,`"version": "${version}"`)
 if(file==='package-lock.json')next=next.replace(/("packages"\s*:\s*\{\s*""\s*:\s*\{[\s\S]*?"version"\s*:\s*")[^"]+/,`$1${version}`)
 fs.writeFileSync(target,next)
}
for(const file of ['src-tauri/Cargo.toml','src-tauri/Cargo.lock']) {
 const target=path.join(project,file),raw=fs.readFileSync(target,'utf8')
 fs.writeFileSync(target,raw.replace(file.endsWith('.toml')?/(\[package\][\s\S]*?\nversion\s*=\s*")[^"]+/:/(name = "launcher-platform"\r?\nversion = ")[^"]+/,`$1${version}`))
}
run('git',['init','-b','local-acceptance'],project)
run('git',['config','core.autocrlf','false'],project)
run('git',['add','-A'],project)
run('git',['-c','user.name=Local acceptance','-c','user.email=acceptance@example.invalid','-c','commit.gpgsign=false','-c','core.hooksPath=','commit','-m','Local silent update acceptance'],project)
const commit=run('git',['rev-parse','HEAD'],project).trim()
assert.equal(run('git',['remote'],project).trim(),'')
const report={version,commit,baseCommit:run('git',['rev-parse','HEAD']).trim(),work,project,evidence,source,githubPublished:false}
fs.writeFileSync(path.join(evidence,'source.json'),JSON.stringify(report,null,2))
fs.writeFileSync(path.join(root,'outputs/acceptance/latest-silent-update-build.json'),JSON.stringify({evidence,project,work,version,commit},null,2))
console.log(JSON.stringify({evidence,project,version,commit}))
const ps=path.join(process.env.SystemRoot,'System32/WindowsPowerShell/v1.0/powershell.exe')
const log=fs.openSync(path.join(evidence,'build.log'),'w')
const child=spawn(ps,['-NoProfile','-ExecutionPolicy','Bypass','-File',path.join(project,'scripts/release-build.ps1'),'-TagName',`v${version}`,'-SourceCommit',commit,'-OutputDirectory',path.join(evidence,'packages'),'-InstallDependencies','-SkipTests'],{cwd:project,env,windowsHide:true,stdio:['ignore',log,log]})
const code=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('exit',resolve)})
fs.closeSync(log)
report.exitCode=code
if(code===0) {
 const bin=path.join(project,'src-tauri/target/release')
 const to=path.join(evidence,'binaries');fs.mkdirSync(to)
 for(const file of ['launcher-platform.exe','launcher-sidecar.exe'])fs.copyFileSync(path.join(bin,file),path.join(to,file))
 report.artifacts=fs.readdirSync(path.join(evidence,'packages')).map(name=>({name,sha256:digest(fs.readFileSync(path.join(evidence,'packages',name)))}))
}
fs.writeFileSync(path.join(evidence,'build-result.json'),JSON.stringify(report,null,2))
console.log(JSON.stringify({exitCode:code,evidence}))
process.exitCode=code||0
// Retain snapshot until native acceptance finishes, then remove its verified unique path.
