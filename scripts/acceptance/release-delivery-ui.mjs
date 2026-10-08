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
await writeFile(path.join(fixtureDir,'main.ts'),`import { createApp, h, ref } from 'vue';import ReleaseModal from '${sourceURL}/components/ReleaseModal.vue';import '${sourceURL}/styles.css';window.__LAUNCHER_BASE__='http://127.0.0.1:19999';createApp({setup(){const visible=ref(true);return()=>visible.value?h(ReleaseModal,{app:{id:'fixture',name:'交付验收',status:'stopped'},onClose:()=>{visible.value=false}}):h('button',{onClick:()=>{visible.value=true}},'重新打开发布')}}).mount('#app');`);
// Limit discovery to this harness; the repository can contain large build caches.
const server=await createServer({configFile:false,root:fixtureDir,plugins:[vue()],resolve:{alias:{'@':path.join(root,'src')}},optimizeDeps:{entries:['index.html']},server:{host:'127.0.0.1',port:19482,strictPort:false,open:false,fs:{allow:[root]}}});await server.listen();
const address=server.httpServer.address();
const browser=await chromium.launch({channel:process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge',headless:true});
const context=await browser.newContext({viewport:{width:1280,height:950},locale:'zh-CN'});
const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
const source=JSON.parse(await readFile(path.join(root,'.launcher/release.yaml'),'utf8'));
const config={...source,source:'file',confidence:1,warnings:[],configPath:'.launcher/release.yaml',targets:source.targets.filter(t=>t.runner.type==='local')};
// A cloud definition often leaves OS unspecified. Its paired local definition
// must retain exactly the same product card when changing the build location.
config.targets.push(...config.targets.map(t=>({...t,id:`${t.id}-cloud`,name:t.name.replace(/\s*[·・]\s*本地\s*$/,'')+' · 云端',runner:{type:'git-push',os:['any']},steps:{publish:'tag-push'},delivery:undefined})));
const profile={appId:'fixture',buildMode:'local',syncPolicy:'auto',remoteName:'origin',versionStrategy:'tauri',preReleaseCommand:'',createTag:true,versionMode:'auto'};
const classification={path:'src/change.ts',status:'M',tracked:true,category:'recommend',selectedDefault:true,reasons:[],sources:[],contentFingerprint:'file',baselineKept:true};
const preflight={repoRoot:'C:/fixture',branch:'master',headSha:'a'.repeat(40),remoteName:'origin',remoteUrl:'https://github.com/oooing/rundock',remotes:['origin'],latestTag:'v2.0.20',latestGroupTags:{},commitsSinceTags:{'v2.0.20':1},suggestedVersion:'2.0.21',suggestedVersions:{product:'2.0.21'},versionStrategy:'tauri',versionFiles:['package.json'],currentVersions:{'package.json':'2.0.20'},changes:[{path:'src/change.ts',status:' M',tracked:true,staged:false}],classifications:[classification],aheadCount:0,unpushedChanges:[],blockingIssues:[],canRelease:true,remoteChecked:false,statusFingerprint:'fixture-fingerprint',profile};
const candidate={id:'candidate',status:'ready',accepted:true,canFormal:true,canSaveProgress:true,classifications:[classification],selectedPaths:['src/change.ts'],sensitiveFindings:[],dependencyFindings:[],checkResults:[],warnings:[],fingerprint:'candidate-fingerprint',treeHash:'b'.repeat(40)};
let run={id:'delivery-run',appId:'fixture',repoRoot:'C:/fixture',branch:'master',remoteName:'origin',targetVersion:'2.0.21',tagName:'v2.0.21',createTag:true,pushRemote:true,selectedTargets:[],status:'failed',stage:'delivery_publish',commitSha:'c'.repeat(40),errorCode:'upload_unconfirmed',errorMessage:'上传连接中断，安装包已保存',createdAt:new Date().toISOString(),finishedAt:new Date().toISOString()};
let deliveries=[{runId:run.id,groupId:'product',manifestSha256:'d'.repeat(64),state:'failed',releaseId:1,url:'https://github.com/fixture/project/releases/tag/v2.0.21',errorCode:'upload_unconfirmed',errorMessage:'上传待恢复',syncState:'unconfigured',syncMessage:''}];
const calls=[];let submitted;let localSubmitted;let history=[];
const localRun={...run,id:'local-package-run',intent:'build-only',status:'succeeded',stage:'completed',createTag:false,pushRemote:false,errorCode:'',errorMessage:''};
await context.route('http://127.0.0.1:19999/api/**',async route=>{
 const request=route.request(),url=new URL(request.url()),pathname=url.pathname;calls.push({method:request.method(),path:pathname});
 let body={};
 if(pathname.endsWith('/release/preflight'))body=preflight;
 else if(pathname.endsWith('/release-config'))body=config;
 else if(pathname.endsWith('/release-profile')){if(request.method()==='PATCH')Object.assign(profile,request.postDataJSON());body=profile;}
 else if(pathname.endsWith('/local-builds')&&request.method()==='POST'){localSubmitted=request.postDataJSON();body=localRun;}
 else if(pathname.endsWith('/local-builds'))body={preparation:{projectRoot:'C:/fixture',configFingerprint:'fixture-config',targets:[{id:'local-pc',name:'PC 安装包',kind:'desktop',currentVersion:'2.0.20',build:true,package:true,available:true},{id:'local-android',name:'Android 安装包',kind:'android',currentVersion:'1.0.0',build:true,package:true,available:true}]},recentRuns:[]};
 else if(pathname===`/api/local-builds/${localRun.id}`)body={run:localRun,targets:[],logs:[],artifacts:[],outputDirectory:''};
 else if(pathname.endsWith('/release/notes-draft'))body={text:'验收发布说明',baseTag:'v2.0.20',sourceFingerprint:'fixture-fingerprint'};
 else if(pathname.includes('/release/candidate'))body=candidate;
 else if(pathname.endsWith('/releases')&&request.method()==='POST'){submitted=request.postDataJSON();run={...run,selectedTargets:submitted.selectedTargets};body=run;}
 else if(pathname.endsWith('/releases'))body=history;
 else if(pathname.endsWith('/retry')){run={...run,status:'succeeded',stage:'completed',errorCode:'',errorMessage:''};deliveries=deliveries.map(x=>({...x,state:'published',errorCode:'',errorMessage:'',syncState:'pending'}));body=run;}
 else if(pathname.endsWith('/sync')){deliveries=deliveries.map(x=>({...x,syncState:'verified'}));body={run,deliveries,logs:[],targets:[],artifacts:[]};}
 else if(pathname.endsWith('/cancel')){run={...run,status:'failed',errorCode:'delivery_cancelled'};body={run};}
 else if(pathname===`/api/releases/${run.id}`)body={run,deliveries,logs:[],targets:[],artifacts:[],retryConfirmationRequired:false};
 else if(history.some(item=>pathname===`/api/releases/${item.id}`))body={run:history.find(item=>pathname===`/api/releases/${item.id}`),deliveries:[],logs:[],targets:[],artifacts:[],retryConfirmationRequired:false};
 else {await route.fulfill({status:404,json:{error:'Unexpected acceptance endpoint '+pathname}});return;}
 await route.fulfill({json:body,headers:{'Access-Control-Allow-Origin':'*'}});
});
const report={boundary:'real Vue dialog and browser, simulated HTTP',passed:false,checks:[],requests:calls};
try {
 await page.goto(`http://127.0.0.1:${address.port}/`);
 const githubChoice=page.getByRole('button',{name:'本地构建并发布 本机 → GitHub Release',exact:true});
 const cloudChoice=page.getByRole('button',{name:'云端构建并发布 GitHub → Release',exact:true});
 const localChoice=page.locator('.local-card');
 const chooseLocalVersion=async value=>{
   await localChoice.click();
   await page.locator('.version-option').filter({hasText:value==='current'?'保持当前版本':'升级版本'}).click();
 };
 await githubChoice.waitFor();
 assert.equal(await page.locator('#release-sync-help, .sync-choice code').count(),0);
 assert.equal(await githubChoice.getAttribute('aria-pressed'),'true');
 for(const width of [1280,390]) {
   await page.setViewportSize({width,height:950});
   const rows=await page.evaluate(()=>['.purpose-options','.hybrid-cards'].map(selector=>{
     const row=document.querySelector(selector),r=row.getBoundingClientRect();
     return {left:r.left,right:r.right,cards:[...row.children].map(el=>{
       const c=el.getBoundingClientRect();return {left:c.left,right:c.right,top:c.top,width:c.width,height:c.height};
     })};
   }));
   assert.equal(rows[0].cards.length,2);
   assert.equal(rows[1].cards.length,3);
   for(const row of rows) {
     assert.ok(Math.abs(row.left-rows[0].left)<1 && Math.abs(row.right-rows[0].right)<1,'choice rows align');
     assert.ok(row.cards.every(c=>Math.abs(c.width-row.cards[0].width)<1),'options split equally');
     if(width===1280) assert.ok(row.cards.every(c=>c.top===row.cards[0].top),'desktop cards remain parallel');
     assert.ok(row.cards.every(c=>c.left>=row.left-1&&c.right<=row.right+1),'no narrow-screen overflow');
   }
   await localChoice.click();
   assert.equal(await page.locator('.version-option').count(),2);
   assert.equal(await githubChoice.getAttribute('aria-pressed'),'true','opening a branch does not commit it');
   await page.screenshot({path:path.join(evidence,width===1280?'release-card-cascade-expanded.png':'release-card-cascade-mobile.png'),fullPage:true});
   await page.getByRole('button',{name:'取消版本选择',exact:true}).click();
   assert.equal(await localChoice.getAttribute('aria-expanded'),'false');
   assert.equal(await localChoice.evaluate(el=>el===document.activeElement),true);
   assert.equal(await githubChoice.getAttribute('aria-pressed'),'true');
 }
 await page.setViewportSize({width:1280,height:950});
 await localChoice.click();
 await page.keyboard.press('Escape');
 assert.equal(await localChoice.getAttribute('aria-expanded'),'false');
 await localChoice.click();
 await page.getByRole('heading',{name:'发布 交付验收',exact:true}).click();
 assert.equal(await localChoice.getAttribute('aria-expanded'),'false');
 report.checks.push('three equal parallel cards with a two-leaf local cascade; cancel, Escape and outside click preserve the selected plan; no mobile overflow');
 await page.screenshot({path:path.join(evidence,'release-choice-layout.png'),fullPage:true});
 const productCards=()=>page.locator('.version-platform-card').evaluateAll(cards=>cards.map(card=>({
   icon:card.querySelector('.platform-icon')?.textContent,
   name:card.querySelector('.platform-title strong')?.textContent,
   detail:card.querySelector('.platform-copy small')?.textContent,
 })));
 const localCards=await productCards();assert.ok(localCards.length);
 const assertTwoLineCards=async()=>{
   const rows=await page.locator('.platform-copy').evaluateAll(cards=>cards.map(card=>{
     const title=card.querySelector('.platform-title'),meta=card.querySelector('.platform-meta');
     const version=meta?.querySelector('.platform-current-version'),description=meta?.querySelector('.platform-description');
     return {titleVersions:title.querySelectorAll('.platform-current-version').length,
       titleBottom:title.getBoundingClientRect().bottom,metaTop:meta.getBoundingClientRect().top,
       metaHeight:meta.getBoundingClientRect().height,lineHeight:parseFloat(getComputedStyle(meta).lineHeight),
       version:version?.textContent,versionRight:version?.getBoundingClientRect().right,
       descriptionLeft:description?.getBoundingClientRect().left};
   }));
   assert.ok(rows.length);
   for(const row of rows){
     assert.equal(row.titleVersions,0);assert.ok(row.version);
     assert.ok(row.metaTop>=row.titleBottom,'version is on the second line');
     assert.ok(Math.abs(row.metaHeight-row.lineHeight)<1,'metadata stays on one line');
     if(row.descriptionLeft!==undefined) assert.ok(row.descriptionLeft-row.versionRight>=5,'space separates version and description');
   }
 };
 await assertTwoLineCards();
 await cloudChoice.click();
 assert.equal(await page.locator('.local-build-options').count(),0,'cloud has no redundant destination options');
 assert.match(await page.locator('.publish-submit').innerText(),/云端构建并发布/);
 assert.deepEqual(await productCards(),localCards);
 await assertTwoLineCards();
 report.checks.push('platform title is the first line; version precedes description with spacing on a single second line in both build modes');
 await githubChoice.click();
 assert.equal(await githubChoice.getAttribute('aria-pressed'),'true');
 assert.match(await page.locator('.publish-submit').innerText(),/^构建并发布/);
 assert.deepEqual(await productCards(),localCards);
 report.checks.push('cloud/local publish action labels match selected cards; both retain identical platform cards');
 const remembered = async predicate => page.waitForFunction(predicate);
 await page.locator('.platform-select').click();
 await remembered(()=>JSON.parse(localStorage.getItem('launcher.release-preferences.fixture')||'{}').releaseTargets?.local?.length===0);
 await page.reload();await githubChoice.waitFor();await page.locator('.platform-select').waitFor();
 assert.equal(await page.locator('.platform-select').getAttribute('aria-pressed'),'false');
 await page.locator('.platform-select').click();
 assert.equal(await page.getByRole('checkbox',{name:'只打包，不发布新版本'}).count(),0);
 await chooseLocalVersion('current');
 assert.equal(await page.locator('#release-sync-help, .sync-choice code').count(),0);
 await page.getByRole('button',{name:'仅本地打包',exact:true}).waitFor();
 // Cloud/local publication are explicit; opening and cancelling packaging leaves cannot opt into uploads.
 await cloudChoice.click();
 assert.equal(await cloudChoice.getAttribute('aria-pressed'),'true');
 await remembered(()=>{const p=JSON.parse(localStorage.getItem('launcher.release-preferences.fixture')||'{}');return p.buildMode==='github'&&p.syncPolicy==='auto'&&p.localSyncPolicy==='local'});
 await page.reload();await cloudChoice.waitFor();
 assert.equal(await cloudChoice.getAttribute('aria-pressed'),'true');
 await localChoice.press('Enter');
 await page.keyboard.press('Escape');
 assert.equal(await cloudChoice.getAttribute('aria-pressed'),'true');
 await chooseLocalVersion('current');
 assert.equal(calls.filter(x=>x.method==='POST'&&/\/(?:releases|local-builds)$/.test(x.path)).length,0);
 report.checks.push('explicit card choice and reload preserve preferences; opening/cancelling never starts tasks or changes the plan');
 assert.match(await localChoice.innerText(),/保持当前版本/);
 await page.locator('#local-build-target-local-pc').uncheck();
 await page.locator('#local-build-target-local-android').check();
 await remembered(()=>JSON.parse(localStorage.getItem('launcher.release-preferences.fixture')||'{}').packagingTargets?.join() === 'local-android');
 await page.locator('.m-head button').click();
 await page.getByRole('button',{name:'重新打开发布',exact:true}).click();
 await page.locator('#local-build-target-local-android').waitFor();
 assert.equal(await page.locator('#local-build-target-local-android').isChecked(),true);
 assert.equal(await page.locator('#local-build-target-local-pc').isChecked(),false);
 await page.locator('#local-build-target-local-android').uncheck();
 await remembered(()=>JSON.parse(localStorage.getItem('launcher.release-preferences.fixture')||'{}').packagingTargets?.length === 0);
 await page.reload();await page.locator('#local-build-target-local-pc').waitFor();
 assert.equal(await page.locator('#local-build-target-local-pc').isChecked(),false);
 assert.equal(await page.getByRole('button',{name:'仅本地打包',exact:true}).isDisabled(),true);
 await page.locator('#local-build-target-local-pc').check();
 await chooseLocalVersion('upgrade');
 await page.getByRole('radio',{name:'手动设置',exact:true}).check();
 await page.getByRole('radio',{name:'仅提交代码',exact:true}).check();
 await remembered(()=>{const p=JSON.parse(localStorage.getItem('launcher.release-preferences.fixture')||'{}');return p.releaseIntent==='save-progress'&&p.localVersionMode==='upgrade'&&p.versionMode==='manual'});
 await page.reload();await page.getByRole('radio',{name:'仅提交到本机',exact:true}).waitFor();
 assert.equal(await page.getByRole('radio',{name:'仅提交代码',exact:true}).isChecked(),true);
 await page.getByRole('radio',{name:'发布版本',exact:true}).check();
 assert.match(await localChoice.innerText(),/升级版本/);
 assert.equal(await page.getByRole('radio',{name:'手动设置',exact:true}).isChecked(),true);
 await page.getByRole('radio',{name:'自动递增',exact:true}).check();
 await chooseLocalVersion('current');
 await remembered(()=>{const p=JSON.parse(localStorage.getItem('launcher.release-preferences.fixture')||'{}');return p.localVersionMode==='current'&&p.versionMode==='auto'});
 assert.equal(await page.locator('.preference-save-state').count(),0);
 report.checks.push('reopen/reload restores choices, including empty selections; successful autosave has no visible hint');
 assert.equal(await page.locator('.build-choice').count(),1,'build location remains accessible from local packaging');
 assert.equal(await page.locator('.publish-submit').count(),0);
 assert.equal(calls.filter(x=>x.path.endsWith('/releases')&&x.method==='POST').length,0);
 await localChoice.press('Enter');
 await page.keyboard.press('End');
 await page.keyboard.press('Enter');
 await page.locator('.publish-submit').waitFor();
 assert.match(await localChoice.innerText(),/升级版本/);
 await localChoice.press('ArrowDown');
 await page.keyboard.press('Home');
 await page.keyboard.press('Enter');
 await page.getByRole('button',{name:'仅本地打包',exact:true}).click();
 await page.locator('.local-build-status.succeeded').waitFor();
 assert.equal(await page.locator('.local-build-status').innerText(),'本地构建完成');
 assert.deepEqual(localSubmitted.targetIds,['local-pc']);
 assert.equal(localSubmitted.configFingerprint,'fixture-config');
 assert.equal(Object.hasOwn(localSubmitted,'pushRemote'),false);
 assert.equal(calls.filter(x=>x.path.endsWith('/releases')&&x.method==='POST').length,0);
 report.checks.push('local-only defaults to unchanged version, supports keyboard version choice and submits only the isolated local-build endpoint');
 await page.screenshot({path:path.join(evidence,'local-destination-complete.png'),fullPage:true});
 await page.getByRole('radio',{name:'仅提交代码',exact:true}).check();
 assert.equal(await page.getByRole('radio',{name:'仅提交到本机',exact:true}).isChecked(),true);
 assert.equal(await page.locator('.local-version-choice').count(),0);
 assert.equal(await page.locator('.build-choice').count(),0);
 await page.getByRole('radio',{name:'发布版本',exact:true}).check();
 await githubChoice.click();
 await page.locator('.build-choice').waitFor();
 assert.equal(await githubChoice.getAttribute('aria-pressed'),'true');
 report.checks.push('local upload choice restores formal release controls; code-only continues to hide build controls');
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
 // "Publish" is a new configuration entry. Completed runs are opened only from history,
 // even if one was last viewed or remains in sessionStorage after reload.
 const oldRun={...run,id:'old-failed-run',tagName:'v2.0.19',status:'failed',stage:'target_build',
   errorCode:'release_commit_changed',errorMessage:'旧任务：构建命令改变了 Git 提交',createdAt:'2026-10-05 09:11:08'};
 history=[run,oldRun];
 const reopen=async()=>{
   await page.locator('.m-head button').click();
   await page.getByRole('button',{name:'重新打开发布',exact:true}).click();
   await githubChoice.waitFor();
   assert.equal(await page.locator('.release-progress-overview').count(),0);
   assert.equal(await page.locator('.retry-submit').count(),0);
 };
 await reopen();await reopen();
 await page.evaluate(()=>sessionStorage.setItem('launcher.release-session:http://127.0.0.1:19999',JSON.stringify({appId:'fixture',runId:'old-failed-run'})));
 await page.reload();
 await githubChoice.waitFor();
 assert.equal(await page.locator('.release-progress-overview').count(),0);
 assert.equal(await page.getByText(oldRun.errorMessage,{exact:true}).count(),0);
 report.checks.push('reopening twice and reloading with a stale failed-run session opens a new release form');
 await page.screenshot({path:path.join(evidence,'reopened-new-release.png'),fullPage:true});
 await page.locator('.history-panel summary').click();
 await page.getByRole('button',{name:'查看 v2.0.21 的发布记录',exact:true}).click();
 await page.getByText('构建与发布已完成',{exact:true}).waitFor();
 await page.getByRole('button',{name:'准备新发布',exact:true}).click();
 await githubChoice.waitFor();
 await page.locator('.history-panel summary').click();
 await page.getByRole('button',{name:'查看 v2.0.19 的发布记录',exact:true}).click();
 await page.locator('.progress-error').filter({hasText:oldRun.errorMessage}).waitFor();
 await reopen();
 report.checks.push('successful and failed history stay manually accessible; returning or reopening only opens the new form');
 const completedRun=run;
 run={...run,status:'running',stage:'target_build',finishedAt:''};history=[run,oldRun];
 await page.locator('.m-head button').click();
 await page.getByRole('button',{name:'重新打开发布',exact:true}).click();
 await page.locator('.release-progress-overview.running').waitFor();
 const fixedCancel=page.locator('.release-progress-overview').getByRole('button',{name:'取消执行',exact:true});
 assert.equal(await page.getByRole('button',{name:'取消执行',exact:true}).count(),1);
 assert.ok((await fixedCancel.boundingBox()).height>=44);
 const cancelPosition=await fixedCancel.boundingBox();
 await page.locator('.m-body').evaluate(el=>{el.scrollTop=el.scrollHeight});
 assert.deepEqual(await fixedCancel.boundingBox(),cancelPosition);
 await page.locator('.m-body').evaluate(el=>{el.scrollTop=0});
 await page.screenshot({path:path.join(evidence,'release-running-controls.png')});
 report.checks.push('one large cancel button stays fixed in the actual release modal while details scroll');
 assert.equal(await page.locator('.publish-submit').count(),0);
 run=completedRun;history=[run,oldRun];
 await page.getByText('构建与发布已完成',{exact:true}).waitFor();
 await reopen();
 assert.equal(calls.filter(x=>x.path.endsWith('/releases')&&x.method==='POST').length,1);
 assert.equal(calls.filter(x=>x.path.endsWith('/retry')&&x.method==='POST').length,1);
 report.checks.push('active work resumes without duplicate submission; after completion reopening returns to configuration');
 await page.evaluate(()=>localStorage.setItem('rundock.ui.locale','en'));
 await page.reload();
 await page.locator('.local-card').waitFor();
 assert.match(await page.locator('.build-choice').innerText(),/Cloud build and publish/);
 await page.locator('.local-card').click();
 await page.locator('.version-popover').waitFor();
 assert.doesNotMatch(await page.locator('.build-choice').innerText(),/[\u3400-\u9fff]/);
 report.checks.push('all card and cascade labels translate to English');
 assert.deepEqual(errors,[]);report.passed=true;
} catch(error){report.error=String(error);report.pageErrors=errors;await page.screenshot({path:path.join(evidence,'delivery-ui-failure.png'),fullPage:true});throw error;}
finally {await writeFile(path.join(evidence,'delivery-ui.json'),JSON.stringify(report,null,2));await context.close();await browser.close();await server.close();}
