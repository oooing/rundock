// Browser acceptance of the real release dialog. HTTP is simulated here;
// the Go acceptance independently exercises Git/SQLite/build/delivery behavior.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createServer } from 'vite';
import vue from '@vitejs/plugin-vue';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(import.meta.url);
const { chromium }=require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright');
const evidence=path.join(root,'outputs/release-delivery');await mkdir(evidence,{recursive:true});
const fixtureDir=path.join(root,'.tmp/delivery-ui');await mkdir(fixtureDir,{recursive:true});
await writeFile(path.join(fixtureDir,'index.html'),'<html lang="zh"><head><meta charset="utf-8"></head><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>');
const sourceURL='/@fs/'+root.replaceAll('\\','/')+'/src';
await writeFile(path.join(fixtureDir,'main.ts'),`import { createApp, h } from 'vue';import ReleaseModal from '${sourceURL}/components/ReleaseModal.vue';import '${sourceURL}/styles.css';window.__LAUNCHER_BASE__='http://127.0.0.1:19999';createApp({render:()=>h(ReleaseModal,{app:{id:'fixture',name:'交付验收',status:'stopped'},onClose:()=>{}})}).mount('#app');`);
// Limit discovery to this harness; the repository can contain large build caches.
const server=await createServer({configFile:false,root:fixtureDir,plugins:[vue()],resolve:{alias:{'@':path.join(root,'src')}},optimizeDeps:{entries:['index.html']},server:{host:'127.0.0.1',port:19482,strictPort:false,open:false,fs:{allow:[root]}}});await server.listen();
const address=server.httpServer.address();
const browser=await chromium.launch({channel:process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge',headless:true});
const context=await browser.newContext({viewport:{width:1280,height:950}});
const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
const source=JSON.parse(await readFile(path.join(root,'.launcher/release.yaml'),'utf8'));
const config={...source,source:'file',confidence:1,warnings:[],configPath:'.launcher/release.yaml',targets:source.targets.filter(t=>t.runner.type==='local')};
const profile={appId:'fixture',buildMode:'local',remoteName:'origin',versionStrategy:'tauri',preReleaseCommand:'',createTag:true,versionMode:'auto'};
const classification={path:'src/change.ts',status:'M',tracked:true,category:'recommend',selectedDefault:true,reasons:[],sources:[],contentFingerprint:'file',baselineKept:true};
const preflight={repoRoot:'C:/fixture',branch:'master',headSha:'a'.repeat(40),remoteName:'origin',remoteUrl:'https://github.com/oooing/rundock',remotes:['origin'],latestTag:'v2.0.20',latestGroupTags:{},commitsSinceTags:{'v2.0.20':1},suggestedVersion:'2.0.21',suggestedVersions:{product:'2.0.21'},versionStrategy:'tauri',versionFiles:['package.json'],currentVersions:{'package.json':'2.0.20'},changes:[{path:'src/change.ts',status:' M',tracked:true,staged:false}],classifications:[classification],aheadCount:0,unpushedChanges:[],blockingIssues:[],canRelease:true,remoteChecked:false,statusFingerprint:'fixture-fingerprint',profile};
const candidate={id:'candidate',status:'ready',accepted:true,canFormal:true,canSaveProgress:true,classifications:[classification],selectedPaths:['src/change.ts'],sensitiveFindings:[],dependencyFindings:[],checkResults:[],warnings:[],fingerprint:'candidate-fingerprint',treeHash:'b'.repeat(40)};
let run={id:'delivery-run',appId:'fixture',repoRoot:'C:/fixture',branch:'master',remoteName:'origin',targetVersion:'2.0.21',tagName:'v2.0.21',createTag:true,pushRemote:true,selectedTargets:[],status:'failed',stage:'delivery_publish',commitSha:'c'.repeat(40),errorCode:'upload_unconfirmed',errorMessage:'上传连接中断，安装包已保存',createdAt:new Date().toISOString(),finishedAt:new Date().toISOString()};
let deliveries=[{runId:run.id,groupId:'product',manifestSha256:'d'.repeat(64),state:'failed',releaseId:1,url:'https://github.com/fixture/project/releases/tag/v2.0.21',errorCode:'upload_unconfirmed',errorMessage:'上传待恢复',syncState:'unconfigured',syncMessage:''}];
const calls=[];let submitted;
await context.route('http://127.0.0.1:19999/api/**',async route=>{
 const request=route.request(),url=new URL(request.url()),pathname=url.pathname;calls.push({method:request.method(),path:pathname});
 let body={};
 if(pathname.endsWith('/release/preflight'))body=preflight;
 else if(pathname.endsWith('/release-config'))body=config;
 else if(pathname.endsWith('/release-profile'))body=profile;
 else if(pathname.endsWith('/release/notes-draft'))body={text:'验收发布说明',baseTag:'v2.0.20',sourceFingerprint:'fixture-fingerprint'};
 else if(pathname.includes('/release/candidate'))body=candidate;
 else if(pathname.endsWith('/releases')&&request.method()==='POST'){submitted=request.postDataJSON();run={...run,selectedTargets:submitted.selectedTargets};body=run;}
 else if(pathname.endsWith('/releases'))body=[];
 else if(pathname.endsWith('/retry')){run={...run,status:'succeeded',stage:'completed',errorCode:'',errorMessage:''};deliveries=deliveries.map(x=>({...x,state:'published',errorCode:'',errorMessage:'',syncState:'pending'}));body=run;}
 else if(pathname.endsWith('/sync')){deliveries=deliveries.map(x=>({...x,syncState:'verified'}));body={run,deliveries,logs:[],targets:[],artifacts:[]};}
 else if(pathname.endsWith('/cancel')){run={...run,status:'failed',errorCode:'delivery_cancelled'};body={run};}
 else if(pathname===`/api/releases/${run.id}`)body={run,deliveries,logs:[],targets:[],artifacts:[],retryConfirmationRequired:false};
 else {await route.fulfill({status:404,json:{error:'Unexpected acceptance endpoint '+pathname}});return;}
 await route.fulfill({json:body,headers:{'Access-Control-Allow-Origin':'*'}});
});
const report={boundary:'real Vue dialog and browser, simulated HTTP',passed:false,checks:[],requests:calls};
try {
 await page.goto(`http://127.0.0.1:${address.port}/`);
 const choice=page.locator('#delivery-local-windows');await choice.waitFor();
 assert.equal(await choice.inputValue(),'local');await choice.selectOption('github');
 await page.getByText('oooing/rundock · oooing',{exact:false}).waitFor();
 report.checks.push('local build destination independently selectable');
 // The existing notes generator fills the confirmation text; wait for submit availability.
 const submit=page.locator('.publish-submit');await submit.waitFor();
 await page.waitForFunction(()=>!document.querySelector('.publish-submit')?.disabled);
 await submit.click();await page.getByText('上传连接中断，安装包已保存',{exact:true}).waitFor();
 assert.equal(submitted.buildMode,'local');assert.equal(submitted.pushRemote,true);assert.equal(submitted.createTag,true);assert.equal(submitted.selectedTargets[0].publish,true);
 assert.equal(submitted.selectedTargets[0].build,true);
 report.checks.push('submission freezes local build plus GitHub destination');
 await page.screenshot({path:path.join(evidence,'delivery-upload-paused.png'),fullPage:true});
 await page.locator('.retry-submit').click();await page.getByText('构建与发布已完成',{exact:true}).waitFor();
 await page.getByText('等待服务器同步',{exact:true}).waitFor();
 assert.equal(await page.locator('.actions-link').count(),0);
 assert.equal(await page.locator('.completion-banner.pending').count(),0);
 report.checks.push('local delivery does not claim a cloud build or source-only release');
 assert.equal(calls.filter(x=>x.path.endsWith('/releases')&&x.method==='POST').length,1);
 report.checks.push('resume uses retry endpoint without a second create');
 await page.getByRole('button',{name:'重新检查服务器同步'}).click();await page.getByText('服务器版本已核验',{exact:true}).waitFor();
 report.checks.push('build, publish and synchronization outcomes remain separate');
 await page.screenshot({path:path.join(evidence,'delivery-complete.png'),fullPage:true});
 assert.deepEqual(errors,[]);report.passed=true;
} catch(error){report.error=String(error);report.pageErrors=errors;await page.screenshot({path:path.join(evidence,'delivery-ui-failure.png'),fullPage:true});throw error;}
finally {await writeFile(path.join(evidence,'delivery-ui.json'),JSON.stringify(report,null,2));await context.close();await browser.close();await server.close();}
