import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { spawn, spawnSync } from 'node:child_process'
const root=process.cwd(), dir=fs.mkdtempSync(path.join(root,'.tmp','installed-version-'))
function run(tool,args){const r=spawnSync(tool,args,{encoding:'utf8',windowsHide:true});assert.equal(r.status,0,r.stderr+r.stdout)}
const rc=process.env.RC_EXE || 'C:/Program Files (x86)/Windows Kits/10/bin/10.0.19041.0/x64/rc.exe'
for(const patch of [39,40]) {
 const resource=path.join(dir,`${patch}.rc`), res=path.join(dir,`${patch}.res`)
 fs.writeFileSync(resource,`1 VERSIONINFO\nFILEVERSION 2,0,${patch},0\nPRODUCTVERSION 2,0,${patch},0\nBEGIN\nEND\n`)
 run(rc,['/nologo','/fo',res,resource])
 run('rustc.exe',['--edition=2021','scripts/tests/installed-version-host.rs','-o',path.join(dir,`${patch}.exe`),'-C',`link-arg=${res}`])
}
const canonical=path.join(dir,'launcher-platform.exe')
fs.copyFileSync(path.join(dir,'39.exe'),canonical)
const child=spawn(canonical,[],{stdio:['pipe','pipe','pipe'],windowsHide:true})
let output='',errors=''
child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>errors+=b)
const until=async fn=>{for(let i=0;i<100;i++){if(fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timeout: '+errors)}
try {
 await until(()=>output.includes('\n'));assert.equal(output.trim(),'2.0.39')
 fs.renameSync(canonical,path.join(dir,'running-old.exe'))
 fs.copyFileSync(path.join(dir,'40.exe'),canonical)
 child.stdin.end('\n');await until(()=>child.exitCode!==null)
 assert.equal(child.exitCode,0,errors)
 assert.deepEqual(output.trim().split(/\r?\n/),['2.0.39','2.0.40'])
 console.log(JSON.stringify({passed:true,check:'running process detects replaced canonical executable',dir}))
}finally{if(child.exitCode===null)child.kill()}
