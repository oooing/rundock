<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { hasOnlyFileReviewBlock } from '@/utils/releaseIssues'
import { tr } from '@/i18n'
import ReleaseCheckIssues from './ReleaseCheckIssues.vue'
import ReleaseFilePreview from './ReleaseFilePreview.vue'
import type { ReleaseCandidate, ReleaseFileCategory, ReleaseFileClassification, ReleaseManualDecision } from '@/types'
const props = withDefaults(defineProps<{ignoreFile?:(file:ReleaseFileClassification)=>Promise<string>;appId:string;intent:'formal'|'save-progress';checksEnabled?:boolean;locked?:boolean;cloudBuild?:boolean;findingDecisions?:Record<string,'allow'|'exclude'>;resolvingReview?:boolean;files: ReleaseFileClassification[]; selected: Record<string,boolean>; decisions: ReleaseManualDecision[]; candidate: ReleaseCandidate|null; busy: boolean; stale: boolean}>(),{checksEnabled:true,locked:false})
const emit = defineEmits<{'update:checksEnabled':[enabled:boolean]; choose:[file:ReleaseFileClassification,included:boolean]; recommend:[]; cancel:[]; refresh:[]; check:[]; exception:[finding:ReleaseCandidate['sensitiveFindings'][number],reason:string]}>()
const category=ref<ReleaseFileCategory|null>(null)
const previewPath=ref<string|null>(null)
const pendingIgnores=ref<ReleaseFileClassification[]>([])
const bulkIgnore=ref(false)
const ignoredCount=ref(0)
const bulkHelpId=useId()
const bulkHelp=ref<HTMLElement|null>(null),bulkHelpButton=ref<HTMLButtonElement|null>(null),bulkHelpOpen=ref(false)
function closeBulkHelp(){if(bulkHelp.value?.matches(':popover-open'))bulkHelp.value.hidePopover()}
function positionBulkHelp(){
  const panel=bulkHelp.value,button=bulkHelpButton.value
  if(!panel || !button)return
  const rect=button.getBoundingClientRect(),width=Math.min(304,window.innerWidth-32)
  panel.style.width=`${width}px`
  panel.style.left=`${Math.max(16,Math.min(rect.right-width,window.innerWidth-width-16))}px`
  const height=panel.getBoundingClientRect().height
  panel.style.top=`${Math.max(16,rect.bottom+8+height<=window.innerHeight-16?rect.bottom+8:rect.top-height-8)}px`
}
function onBulkHelpToggle(){bulkHelpOpen.value=!!bulkHelp.value?.matches(':popover-open');if(bulkHelpOpen.value)positionBulkHelp()}
function onHelpViewportChange(event:Event){if(event.target instanceof Node && bulkHelp.value?.contains(event.target))return;closeBulkHelp()}
onMounted(()=>{window.addEventListener('resize',onHelpViewportChange);window.addEventListener('scroll',onHelpViewportChange,true)})
onBeforeUnmount(()=>{window.removeEventListener('resize',onHelpViewportChange);window.removeEventListener('scroll',onHelpViewportChange,true)})
const ignoring=ref(false),ignoreError=ref(''),ignoreNotice=ref('')
async function confirmIgnore(){
  if(!pendingIgnores.value.length || ignoring.value || props.busy || props.locked || !props.ignoreFile) return
  ignoring.value=true;ignoreError.value=''
  try{
    for(const file of [...pendingIgnores.value]){
      const current=props.files.find(item=>item.path===file.path && item.contentFingerprint===file.contentFingerprint)
      if(!current || current.tracked) throw new Error(tr('文件已变化，请重新选择后再忽略。'))
      const ignoreFile=await props.ignoreFile(current)
      pendingIgnores.value=pendingIgnores.value.filter(item=>item.path!==file.path)
      ignoredCount.value+=1
      ignoreNotice.value=bulkIgnore.value?tr('已忽略 {0} 个，剩余 {1} 个。删除忽略规则可恢复。',[ignoredCount.value,pendingIgnores.value.length]):tr('已加入 {0}；以后不再列出此文件。撤销时请删除对应忽略规则。',[ignoreFile])
    }
    bulkIgnore.value=false
  }catch(error){ignoreError.value=error instanceof Error?error.message:String(error)}
  finally{ignoring.value=false}
}
const fileList=ref<HTMLDivElement|null>(null)
async function showPendingFiles(){
  category.value='review'
  await nextTick()
  fileList.value?.scrollIntoView({block:'center'})
  fileList.value?.querySelector<HTMLSelectElement>('select')?.focus({preventScroll:true})
}
function reviewDecision(file:ReleaseFileClassification){
  return props.decisions.find(item=>item.path===file.path && item.contentFingerprint===file.contentFingerprint)?.decision
}
const categories = computed<Array<{id:ReleaseFileCategory;label:string}>>(()=>[
  {id:'review',label:tr('待确认')},{id:'recommend',label:tr('推荐提交')},{id:'local',label:tr('本地资料')},{id:'sensitive',label:tr('敏感阻断')},
])
const groups = computed(() => categories.value.map(category=> ({...category, files:props.files.filter(file=>file.category===category.id),
  selectedCount:props.files.filter(file=>file.category===category.id && props.selected[file.path]).length})))
const unresolved = computed(()=>props.files.filter(file=>file.category==='review' && !props.decisions.some(decision=>decision.path===file.path && decision.contentFingerprint===file.contentFingerprint)))
const activeCategory = computed(()=>category.value ?? (unresolved.value.length ? 'review' : 'recommend'))
watch(activeCategory,closeBulkHelp)
const visibleFiles = computed(()=>groups.value.find(group=>group.id===activeCategory.value)?.files ?? [])
const reviewFiles=computed(()=>props.files.filter(file=>file.category==='review'))
const canBulkIgnore=computed(()=>!!props.ignoreFile && reviewFiles.value.length>0 && reviewFiles.value.every(file=>!file.tracked))
const bulkDecision=computed(()=>{
  if(bulkIgnore.value && pendingIgnores.value.length) return 'ignore'
  if(reviewFiles.value.every(file=>reviewDecision(file)==='exclude')) return 'exclude'
  if(reviewFiles.value.every(file=>reviewDecision(file)==='include')) return 'include'
  return ''
})
watch([()=>props.files,()=>props.decisions,()=>props.busy,()=>props.locked,ignoring,pendingIgnores],()=>{
  if(props.busy || props.locked || ignoring.value || pendingIgnores.value.length) return
  const undecided=unresolved.value
  if(undecided.length && category.value===null) category.value='review'
  for(const file of undecided) emit('choose',file,false)
},{immediate:true})
const displayStatus=computed(()=>props.busy?'running':hasOnlyFileReviewBlock(props.candidate)?'review':props.candidate?.intent==='formal' && (props.candidate.sensitiveFindings.length || props.candidate.dependencyFindings.some(item=>item.blocked))?'blocked':props.candidate?.status||'pending')
const labels:Record<string,string>={review:tr('待确认文件'),pending:tr('提交时自动检查'),running:tr('检查中'),passed:tr('检查通过'),failed:tr('检查失败'),cancelled:tr('已取消'),unverified:tr('未验证'),stale:tr('已失效'),blocked:tr('已阻断'),ready:tr('可以提交'),skipped:tr('已跳过')}
function cancelIgnore(){pendingIgnores.value=[];bulkIgnore.value=false;ignoredCount.value=0;ignoreError.value=''}
function chooseBulk(event:Event){
  if(props.busy || props.locked || ignoring.value) return
  const value=(event.target as HTMLSelectElement).value
  category.value='review';cancelIgnore();ignoreNotice.value=''
  if(value==='ignore'){
    if(canBulkIgnore.value){pendingIgnores.value=[...reviewFiles.value];bulkIgnore.value=true}
    return
  }
  if(value==='include' || value==='exclude') for(const file of reviewFiles.value) emit('choose',file,value==='include')
}
function chooseReview(file:ReleaseFileClassification,event:Event){
  if(props.busy || props.locked || ignoring.value) return
  category.value='review'
  const value=(event.target as HTMLSelectElement).value
  if(value==='ignore'){
    if(!file.tracked && props.ignoreFile){pendingIgnores.value=[file];bulkIgnore.value=false;ignoredCount.value=0;ignoreError.value='';ignoreNotice.value=''}
    return
  }
  cancelIgnore()
  if(value) emit('choose',file,value==='include')
}
</script>
<template>
  <div class="release-safety">
    <p id="release-file-selection-help" class="file-selection-help"><strong>{{tr('待保存的改动')}}</strong><span>{{tr('未存入代码记录的改动含新增和修改；建议保存默认选中，待确认默认跳过。')}}</span></p>
    <div class="file-categories">
    <div class="category-counts" :aria-label="tr('文件分类')"><button v-for="group in groups.filter(group=>group.files.length || group.id===activeCategory)" :key="group.id" :class="group.id" :aria-pressed="activeCategory===group.id" @click="category=group.id">{{group.label}} <strong>{{group.id==='recommend'?tr('已选 {0}/{1}',[group.selectedCount,group.files.length]):group.id==='review'?unresolved.length:group.files.length}}</strong></button></div>
    <div class="scope-actions">
      <div v-if="activeCategory==='review' && reviewFiles.length" class="bulk-control"><select class="bulk-select" :value="bulkDecision" :disabled="busy || locked || ignoring" :aria-label="tr('批量操作')" :title="canBulkIgnore?tr('批量操作'):tr('含已保存的文件，不能批量忽略，可逐个选择。')" @change="chooseBulk">
        <option value="" disabled>{{tr('混合选择')}}</option>
        <option value="include">{{tr('批量保存')}}</option><option value="exclude">{{tr('批量跳过')}}</option>
        <option value="ignore" :disabled="!canBulkIgnore">{{tr('批量忽略')}}</option>
      </select>
      <button ref="bulkHelpButton" type="button" class="bulk-help-button" :popovertarget="bulkHelpId" :aria-label="tr('批量操作说明')" :aria-controls="bulkHelpId" :aria-expanded="bulkHelpOpen" @click="positionBulkHelp"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 7.5h.01"/></svg></button>
      <div :id="bulkHelpId" ref="bulkHelp" popover="auto" class="bulk-help-popover" role="note" :aria-label="tr('批量操作说明')" @toggle="onBulkHelpToggle">
        <p class="bulk-help-scope">{{tr('适用于“待确认”的全部文件。')}}</p>
        <dl><div><dt>{{tr('提交')}}</dt><dd>{{tr('将这些改动存入本机代码记录。')}}</dd></div><div><dt>{{tr('本次不提交')}}</dt><dd>{{tr('仅排除本次改动，下次发布仍可能提示。')}}</dd></div><div><dt>{{tr('以后忽略（加入 .gitignore）')}}</dt><dd>{{tr('以后不再列出这些文件，不删除文件。')}}</dd></div></dl>
        <p v-if="!canBulkIgnore" class="bulk-help-limit">{{tr('含已保存的文件，不能批量忽略，可逐个选择。')}}</p>
      </div></div>
      <button v-else :disabled="busy || locked || ignoring" @click="emit('recommend')">{{tr('采用推荐范围')}}</button>
    </div>
    </div>
    <div ref="fileList" class="file-groups" aria-describedby="release-file-selection-help">
        <div v-for="file in visibleFiles" :key="file.path" class="scope-file">
          <div class="file-row" :class="{'review-row':file.category==='review'}">
            <input v-if="file.category!=='review'" type="checkbox" :aria-label="tr('选择文件：{0}',[file.path])" :checked="!!selected[file.path]" :disabled="busy || locked || ignoring || (file.category==='sensitive' && !selected[file.path])" @change="emit('choose',file,($event.target as HTMLInputElement).checked)" />
            <button type="button" class="file-name" :title="tr('点击预览：{0}',[file.path])" :aria-expanded="previewPath===file.path" @click="previewPath=previewPath===file.path?null:file.path"><code>{{file.path}}</code></button>
            <select v-if="file.category==='review'" :value="pendingIgnores.some(item=>item.path===file.path)?'ignore':reviewDecision(file)||''" :disabled="busy || locked || ignoring" :aria-label="tr('文件处理方式：{0}',[file.path])" @change="chooseReview(file,$event)">
              <option value="" disabled>{{tr('请选择')}}</option><option value="include">{{tr('提交')}}</option><option value="exclude">{{tr('本次不提交')}}</option>
              <option v-if="!file.tracked && ignoreFile" value="ignore">{{tr('以后忽略（加入 .gitignore）')}}</option>
            </select>
          </div>
          <p v-if="file.category==='review' && file.tracked">{{tr('已被 Git 跟踪，不能直接永久忽略；本次不提交不会移除远端文件。')}}</p>
          <ReleaseFilePreview v-if="previewPath===file.path" :key="file.contentFingerprint" :app-id="appId" :path="file.path" @close="previewPath=null" />
          <p v-if="file.category==='sensitive'" class="blocked">{{file.reasons.join('；')}}</p>
          <p v-if="file.baselineKept && !selected[file.path]" class="baseline-note">{{tr('基线中的旧内容仍保留；取消勾选只排除本次改动。')}}</p>
        </div>
        <p v-if="!visibleFiles.length" class="empty-files">{{tr('没有匹配的文件')}}</p>
    </div>
    <div v-if="pendingIgnores.length" class="ignore-confirm">
      <code v-for="file in pendingIgnores" :key="file.path">{{file.path}}</code>
      <p>{{tr('仅将这个文件加入适用的 .gitignore，不删除文件、不自动提交；以后不再出现在待提交列表中。')}}</p>
      <p>{{tr('忽略规则本身可随项目提交；要恢复此文件，请删除对应规则。')}}</p>
      <button type="button" :disabled="busy || locked || ignoring" @click="confirmIgnore">{{ignoring?tr('正在加入…'):tr('确认加入忽略清单')}}</button>
      <button type="button" :disabled="ignoring" @click="cancelIgnore">{{tr('取消')}}</button>
      <p v-if="ignoreError" role="alert">{{ignoreError}}</p>
    </div>
    <p v-if="ignoreNotice" class="ignore-notice" role="status">{{ignoreNotice}}</p>
    <slot name="after-files" />
    <div class="candidate-result" aria-live="polite" tabindex="-1">
      <div class="safety-head"><label class="check-toggle"><input type="checkbox" role="switch" :checked="checksEnabled" :disabled="busy || locked" aria-describedby="release-check-help" @change="emit('update:checksEnabled',($event.target as HTMLInputElement).checked)" /><strong>{{tr('启用检查')}}</strong></label><span v-if="!checksEnabled || stale || displayStatus !== 'pending'" :class="!checksEnabled?'stale':stale?'stale':displayStatus">{{!checksEnabled?tr('未检查'):stale?tr('需重新检查'):displayStatus==='blocked'?tr('待处理'):labels[displayStatus]||displayStatus}}</span></div>
      <p id="release-check-help">{{checksEnabled?tr('检查敏感信息、遗漏文件和已配置的测试，减少误传与发布失败。'):tr('不检查敏感信息、遗漏文件和测试；Git、版本校验及云端检查不受影响。')}}</p>
      <p v-if="checksEnabled && stale">{{tr('文件或发布范围已变化，下次提交时会自动重新检查。')}}</p>
      <p v-if="checksEnabled && candidate?.accepted && !candidate.checksSkipped && !stale && !busy" class="passed">{{cloudBuild?tr('本地检查已通过。GitHub 构建会在发布后执行，结果另行显示。'):tr('发布前检查已通过。')}}</p>
      <ReleaseCheckIssues v-if="checksEnabled && candidate && !candidate.checksSkipped" :app-id="appId" :candidate="candidate" :files="files" :selected="selected" :busy="busy" :stale="stale" :finding-decisions="findingDecisions" :resolving-review="resolvingReview" @review="showPendingFiles" @choose="(file,included)=>emit('choose',file,included)" @check="emit('check')" @exception="(finding,reason)=>emit('exception',finding,reason)" />
    </div>
  </div>
</template>
<style scoped>
.ignore-confirm{margin:0;padding:12px;border:1px solid var(--border);border-radius:8px;background:var(--bg);font-size:12px;overflow-wrap:anywhere}.ignore-confirm p{margin:8px 0;color:var(--text-faint);line-height:1.6}.ignore-confirm button{margin:4px 8px 0 0;min-height:34px;font-size:12px}.ignore-confirm [role=alert]{color:var(--red)}.ignore-notice{font-size:12px;color:var(--green);line-height:1.6}
.candidate-result .review{color:var(--amber)}
.file-selection-help{display:flex;align-items:baseline;flex-wrap:wrap;gap:4px 12px;margin:0;color:var(--text-faint);font-size:11px;line-height:1.6}.file-selection-help strong{color:var(--text-dim);font-size:12px;font-weight:600}
.ignore-confirm>code{display:block}.bulk-control{display:flex;align-items:center;gap:6px;font-size:11px;color:var(--text-faint)}.bulk-select{width:140px;max-width:100%;padding:6px 9px;font-size:12px;color:var(--text)}.bulk-select:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.bulk-control .bulk-help-button{display:inline-flex;align-items:center;justify-content:center;flex:none;width:28px;height:28px;padding:0;border:0;border-radius:50%;background:transparent;color:var(--text-faint)}.bulk-help-button svg{width:17px;height:17px}.bulk-control .bulk-help-button:hover{background:color-mix(in srgb,var(--accent) 8%,transparent);color:var(--accent)}.bulk-help-button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.bulk-help-popover{position:fixed;inset:auto;margin:0;max-width:calc(100vw - 32px);max-height:calc(100dvh - 32px);overflow:auto;padding:14px;border:1px solid var(--border);border-radius:10px;background:var(--bg-elev-2);color:var(--text);box-shadow:0 8px 24px #0005;font-size:12px;line-height:1.6;overscroll-behavior:contain}.bulk-help-popover p{margin:0;color:var(--text-dim)}.bulk-help-popover dl{display:grid;gap:10px;margin:12px 0 0}.bulk-help-popover dl>div{display:grid;grid-template-columns:60px minmax(0,1fr);gap:10px}.bulk-help-popover dt{font-weight:600}.bulk-help-popover dd{margin:0;color:var(--text-dim)}.bulk-help-popover .bulk-help-limit{margin-top:12px;padding-top:10px;border-top:1px solid var(--border);color:var(--amber)}
@media(pointer:coarse){.bulk-select{min-height:44px}.bulk-control .bulk-help-button{width:44px;height:44px}}
.check-toggle{display:flex;align-items:center;gap:9px;cursor:pointer}.check-toggle input{appearance:none;width:32px;height:18px;border-radius:12px;background:var(--border);position:relative;margin:0;flex-shrink:0;cursor:pointer}.check-toggle input:checked{background:var(--accent)}.check-toggle input:before{content:'';position:absolute;width:12px;height:12px;top:3px;left:3px;border-radius:50%;background:#fff}.check-toggle input:checked:before{left:17px}.check-toggle input:focus-visible{outline:2px solid var(--accent);outline-offset:3px}.check-toggle input:disabled{opacity:.5;cursor:not-allowed}
.release-safety,.release-safety>*{min-width:0}.file-groups{overflow-x:hidden}.scope-file label,.scope-file code{min-width:0}
.file-row{display:flex;align-items:center;gap:8px}.scope-file .file-name{flex:1;min-width:0;text-align:left;background:none;border:0;padding:3px 0;margin:0;color:var(--text);font-size:12px}.scope-file .file-name:hover{color:var(--accent)}.scope-file .file-name code{display:block;overflow-wrap:anywhere;white-space:normal}.file-row>input{flex-shrink:0}
.category-counts>button{padding:6px 9px;border-radius:6px;font-size:12px}.category-counts>button[aria-pressed=true]{border-color:var(--accent);background:var(--bg)}
.file-categories{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.scope-actions{margin-inline-start:auto}.review-row{display:flex;align-items:center;gap:12px;font-size:12px}.review-row select{flex:none;width:112px;padding:7px;font:inherit}.scope-file .review-row code{white-space:normal;overflow-wrap:anywhere}.empty-files{font-size:12px;color:var(--text-faint);text-align:center;padding:12px}
.release-safety{border:1px solid var(--border);border-radius:11px;padding:14px;display:grid;gap:12px}.safety-head{display:flex;align-items:center;justify-content:space-between;gap:12px}.safety-head h3{margin:0;font-size:15px}.safety-head span,small{font-size:11px;color:var(--text-faint)}.category-counts{display:flex;flex-wrap:wrap;gap:8px}.category-counts>span{padding:6px 9px;border-radius:6px;background:var(--bg);font-size:12px}.category-counts strong{margin-left:5px}.scope-actions{display:flex;gap:8px}.scope-actions button,.review-notice button{font-size:12px;padding:6px 9px}.review-notice{display:flex;justify-content:space-between;align-items:center;gap:10px;padding:9px;border-radius:7px;background:rgba(251,191,36,.08);color:var(--amber);font-size:12px}.advanced-files summary,.file-groups summary{cursor:pointer;padding:7px 0;font-size:12px}.advanced-files>input{width:100%;margin:8px 0}.file-groups{max-height:320px;overflow:auto}.scope-file{padding:9px 5px;border-top:1px solid var(--border)}.scope-file label{display:flex;align-items:center;gap:8px;font-size:12px}.scope-file code{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.scope-file p,.candidate-result p{margin:5px 0;font-size:11px;color:var(--text-faint);line-height:1.6}.scope-file p{margin-left:24px}.scope-file button{margin-left:24px;font-size:11px}.candidate-result{border-top:1px solid var(--border);padding-top:12px;display:grid;gap:8px}.candidate-result .passed,.candidate-result .ready{color:var(--green)}.candidate-result .failed,.blocked,.sensitive{color:var(--red)}.candidate-result .stale,.review,.baseline-note{color:var(--amber)}.finding{padding:8px;border:1px solid var(--border);border-radius:6px;font-size:11px;overflow-wrap:anywhere}.check-result summary{display:flex;justify-content:space-between;gap:10px;font-size:12px;cursor:pointer;padding:7px;background:var(--bg);border-radius:6px}.check-result pre{max-height:200px;overflow:auto;font-size:11px;background:var(--bg);padding:9px;white-space:pre-wrap;overflow-wrap:anywhere}
.release-safety { border: 0; border-radius: 0; padding: 0; gap: 16px; }
.file-groups { max-height: 224px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg); scrollbar-gutter: stable; overscroll-behavior: contain; }
.scope-file { padding: 6px 9px; }
.scope-file:first-child { border-top: 0; }
.scope-file .file-name { min-height: 24px; }
.candidate-result { border: 0; padding: 12px 0 0; margin: 0; gap: 4px; }
.candidate-result > p { margin: 2px 0; }
.file-name:focus-visible,.scope-actions button:focus-visible,.category-counts button:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
@media (pointer: coarse) { .scope-file .file-name,.scope-actions button,.category-counts button { min-height: 44px; } }
.review-row select { width: 210px; max-width: 100%; }
.ignore-confirm button:focus-visible,.review-row select:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
@media (max-width: 480px) { .review-row { flex-wrap: wrap; }.review-row .file-name { flex-basis: 100%; }.review-row select { width: 100%; }.ignore-confirm button { margin-bottom: 6px; min-height: 40px; } }
</style>
