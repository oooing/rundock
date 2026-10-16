// Local acceptance packaging only; no git commit, tag, push or release.
import fs from 'node:fs'
const version=process.argv[2]
if(!/^2\.0\.(39|40|41)$/.test(version)) throw Error('Unexpected acceptance version')
for(const file of ['package.json','package-lock.json','src-tauri/tauri.conf.json']) {
 const raw=fs.readFileSync(file,'utf8'), data=JSON.parse(raw)
 const old=data.version
 let changed=raw.replace(`"version": "${old}"`,`"version": "${version}"`)
 if(file==='package-lock.json') changed=changed.replace(/("packages"\s*:\s*\{\s*""\s*:\s*\{[\s\S]*?"version"\s*:\s*")[^"]+/,`$1${version}`)
 fs.writeFileSync(file,changed)
}
for(const file of ['src-tauri/Cargo.toml','src-tauri/Cargo.lock']) {
 let raw=fs.readFileSync(file,'utf8')
 if(file.endsWith('.toml')) raw=raw.replace(/(\[package\][\s\S]*?\nversion\s*=\s*")[^"]+/,`$1${version}`)
 else raw=raw.replace(/(name = "launcher-platform"\r?\nversion = ")[^"]+/,`$1${version}`)
 fs.writeFileSync(file,raw)
}
console.log(`Local acceptance version: ${version}`)
