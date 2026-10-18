// Reuse the isolated build cache after a reviewed correction; no remote writes.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import { execFileSync, spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
const root=process.cwd(), pointer=path.join(root,'outputs/acceptance/latest-silent-update-build.json')
const build=JSON.parse(fs.readFileSync(pointer,'utf8')), {evidence,project,version}=build
assert.equal(path.dirname(project),build.work)
assert.ok(path.basename(build.work).startsWith('rd-update-'))
const env={...process.env,GOPATH:path.join(os.homedir(),'gopath'),CARGO_BUILD_JOBS:'2',GH_TOKEN:'',GITHUB_TOKEN:'',GIT_TERMINAL_PROMPT:'0'}
const run=(exe,args)=>execFileSync(exe,args,{cwd:project,env,encoding:'utf8',windowsHide:true,maxBuffer:16e6,timeout:180000})
assert.equal(run('git',['remote']).trim(),'')
const digest=b=>createHash('sha256').update(b).digest('hex')
const report=JSON.parse(fs.readFileSync(path.join(evidence,'build-result.json'),'utf8'))
const versionFiles=new Set(['package.json','package-lock.json','src-tauri/Cargo.toml','src-tauri/Cargo.lock','src-tauri/tauri.conf.json'])
const updated=[]
for(const [file,hash] of Object.entries(report.source)){
 if(versionFiles.has(file))continue
 const from=path.join(root,file);assert.ok(fs.existsSync(from),`Missing reviewed input ${file}`)
 const bytes=fs.readFileSync(from),next=digest(bytes)
 if(next===hash)continue
 fs.writeFileSync(path.join(project,file),bytes);report.source[file]=next;updated.push(file)
}
run('git',['add','-A'])
run('git',['-c','user.name=Local acceptance','-c','user.email=acceptance@example.invalid','-c','commit.gpgsign=false','-c','core.hooksPath=','commit','-m','Harden installer progress failure recovery'])
report.previousCommit=report.commit;report.commit=run('git',['rev-parse','HEAD']).trim();build.commit=report.commit
fs.writeFileSync(pointer,JSON.stringify(build,null,2))
console.log(JSON.stringify({updated,commit:report.commit}))
const log=fs.openSync(path.join(evidence,'rebuild.log'),'w')
const child=spawn(path.join(process.env.SystemRoot,'System32/WindowsPowerShell/v1.0/powershell.exe'),['-NoProfile','-ExecutionPolicy','Bypass','-File',path.join(project,'scripts/release-build.ps1'),'-TagName',`v${version}`,'-SourceCommit',report.commit,'-OutputDirectory',path.join(evidence,'packages'),'-SkipTests'],{cwd:project,env,windowsHide:true,stdio:['ignore',log,log]})
const code=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('exit',resolve)})
fs.closeSync(log);report.exitCode=code
if(code===0){
 for(const name of ['launcher-platform.exe','launcher-sidecar.exe'])fs.copyFileSync(path.join(project,'src-tauri/target/release',name),path.join(evidence,'binaries',name))
 report.artifacts=fs.readdirSync(path.join(evidence,'packages')).map(name=>({name,sha256:digest(fs.readFileSync(path.join(evidence,'packages',name)))}))
}
fs.writeFileSync(path.join(evidence,'build-result.json'),JSON.stringify(report,null,2))
console.log(JSON.stringify({exitCode:code,evidence}));process.exitCode=code||0
