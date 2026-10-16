import fs from 'node:fs'
const output='outputs/acceptance/production-update-monitor.json', stop='outputs/acceptance/production-update-monitor.stop'
const baseline=JSON.parse(fs.readFileSync('outputs/acceptance/production-update-before.json','utf8').replace(/^\uFEFF/,''))
const active=baseline.apps.filter(a=>a.status==='running'||a.status==='degraded')
const result={startedAt:new Date().toISOString(),samples:0,failures:[],activeBefore:active,instanceBefore:baseline.health.instanceId,last:null}
while(!fs.existsSync(stop)) {
 const at=new Date().toISOString()
 try {
  const health=await (await fetch('http://127.0.0.1:17654/api/health',{signal:AbortSignal.timeout(5000)})).json()
  const apps=await (await fetch('http://127.0.0.1:17654/api/apps',{signal:AbortSignal.timeout(5000)})).json()
  if(health.instanceId!==baseline.health.instanceId)throw Error('Production backend changed')
  for(const old of active){const current=apps.find(a=>a.id===old.id);if(!current||current.pid!==old.pid||current.runId!==old.runId||!['running','degraded'].includes(current.status))throw Error('Project changed: '+old.name)}
  for(const url of ['http://127.0.0.1:17656/','http://127.0.0.1:17655/api/health']){const r=await fetch(url,{signal:AbortSignal.timeout(5000)});if(!r.ok)throw Error(url+' '+r.status)}
  result.last={at,instanceId:health.instanceId,active:apps.filter(a=>a.pid).map(({id,pid,runId,status})=>({id,pid,runId,status}))}
 }catch(e){result.failures.push({at,error:String(e)})}
 result.samples++;fs.writeFileSync(output,JSON.stringify(result,null,2));await new Promise(r=>setTimeout(r,1000))
}
result.finishedAt=new Date().toISOString();fs.writeFileSync(output,JSON.stringify(result,null,2));console.log(JSON.stringify(result))
