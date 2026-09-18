<script setup lang="ts">
import { computed, ref } from 'vue'
import { tr } from '@/i18n'
import ReleaseCheckIssues from './ReleaseCheckIssues.vue'
import ReleaseFilePreview from './ReleaseFilePreview.vue'
import type { ReleaseCandidate, ReleaseFileCategory, ReleaseFileClassification, ReleaseManualDecision } from '@/types'
const props = withDefaults(defineProps<{appId:string;intent:'formal'|'save-progress';checksEnabled?:boolean;locked?:boolean;cloudBuild?:boolean;findingDecisions?:Record<string,'allow'|'exclude'>;resolvingReview?:boolean;files: ReleaseFileClassification[]; selected: Record<string,boolean>; decisions: ReleaseManualDecision[]; candidate: ReleaseCandidate|null; busy: boolean; stale: boolean}>(),{checksEnabled:true,locked:false})
const emit = defineEmits<{'update:checksEnabled':[enabled:boolean]; choose:[file:ReleaseFileClassification,included:boolean]; recommend:[]; cancel:[]; refresh:[]; check:[]; exception:[finding:ReleaseCandidate['sensitiveFindings'][number],reason:string]}>()
const search=ref('')
const category=ref<ReleaseFileCategory|null>(null)
const previewPath=ref<string|null>(null)
function reviewDecision(file:ReleaseFileClassification){
  return props.decisions.find(item=>item.path===file.path && item.contentFingerprint===file.contentFingerprint)?.decision
}
const categories = computed<Array<{id:ReleaseFileCategory;label:string}>>(()=>[
  {id:'review',label:tr('待确认')},{id:'recommend',label:tr('推荐提交')},{id:'local',label:tr('本地资料')},{id:'sensitive',label:tr('敏感阻断')},
])
const groups = computed(() => categories.value.map(category=> ({...category, files:props.files.filter(file=>file.category===category.id),
  visible:props.files.filter(file=>file.category===category.id && (file.path+' '+file.reasons.join(' ')).toLowerCase().includes(search.value.toLowerCase()))})))
const unresolved = computed(()=>props.files.filter(file=>file.category==='review' && !props.decisions.some(decision=>decision.path===file.path && decision.contentFingerprint===file.contentFingerprint)))
const activeCategory = computed(()=>category.value ?? (unresolved.value.length ? 'review' : 'recommend'))
const visibleFiles = computed(()=>groups.value.find(group=>group.id===activeCategory.value)?.visible ?? [])
const displayStatus=computed(()=>props.busy?'running':props.candidate?.intent==='formal' && (props.candidate.sensitiveFindings.length || props.candidate.dependencyFindings.some(item=>item.blocked))?'blocked':props.candidate?.status||'pending')
const labels:Record<string,string>={pending:tr('提交时自动检查'),running:tr('检查中'),passed:tr('检查通过'),failed:tr('检查失败'),cancelled:tr('已取消'),unverified:tr('未验证'),stale:tr('已失效'),blocked:tr('已阻断'),ready:tr('可以提交'),skipped:tr('已跳过')}
function excludePending(){ category.value='review';for(const file of unresolved.value) emit('choose',file,false) }
function chooseReview(file:ReleaseFileClassification,event:Event){
  category.value='review'
  const value=(event.target as HTMLSelectElement).value
  if(value) emit('choose',file,value==='include')
}
</script>
<template>
  <section class="release-safety" :aria-label="tr('发布范围与检查')">
    <div class="safety-head"><h3>{{tr('提交文件')}}</h3><span>{{tr('已选 {0} 项',[Object.values(selected).filter(Boolean).length])}}</span></div>
    <div class="category-counts" :aria-label="tr('文件分类')"><button v-for="group in groups.filter(group=>group.files.length || group.id===activeCategory)" :key="group.id" :class="group.id" :aria-pressed="activeCategory===group.id" @click="category=group.id;search=''">{{group.label}} <strong>{{group.id==='review'?unresolved.length:group.files.length}}</strong></button></div>
    <div class="file-toolbar">
      <input v-model="search" type="search" :placeholder="tr('搜索文件')" :aria-label="tr('搜索文件')" />
      <button :disabled="busy" @click="emit('refresh')">{{tr('刷新')}}</button>
      <button v-if="activeCategory==='review' && unresolved.length" :disabled="busy" @click="excludePending">{{tr('待确认项全部留本地')}}</button>
      <button v-else :disabled="busy" @click="emit('recommend')">{{tr('采用推荐范围')}}</button>
    </div>
    <div class="file-groups">
        <div v-for="file in visibleFiles" :key="file.path" class="scope-file">
          <div class="file-row" :class="{'review-row':file.category==='review'}">
            <input v-if="file.category!=='review'" type="checkbox" :aria-label="tr('选择文件：{0}',[file.path])" :checked="!!selected[file.path]" :disabled="busy || (file.category==='sensitive' && !selected[file.path])" @change="emit('choose',file,($event.target as HTMLInputElement).checked)" />
            <button type="button" class="file-name" :title="tr('点击预览：{0}',[file.path])" :aria-expanded="previewPath===file.path" @click="previewPath=previewPath===file.path?null:file.path"><code>{{file.path}}</code></button>
            <select v-if="file.category==='review'" :value="reviewDecision(file)||''" :disabled="busy" :aria-label="tr('文件处理方式：{0}',[file.path])" @change="chooseReview(file,$event)">
              <option value="" disabled>{{tr('请选择')}}</option><option value="include">{{tr('提交')}}</option><option value="exclude">{{tr('留在本地')}}</option>
            </select>
          </div>
          <ReleaseFilePreview v-if="previewPath===file.path" :key="file.contentFingerprint" :app-id="appId" :path="file.path" @close="previewPath=null" />
          <p v-if="file.category==='sensitive'" class="blocked">{{file.reasons.join('；')}}</p>
          <p v-if="file.baselineKept && !selected[file.path]" class="baseline-note">{{tr('基线中的旧内容仍保留；取消勾选只排除本次改动。')}}</p>
        </div>
        <p v-if="!visibleFiles.length" class="empty-files">{{tr('没有匹配的文件')}}</p>
    </div>
    <div class="candidate-result" aria-live="polite" tabindex="-1">
      <div class="safety-head"><label class="check-toggle"><input type="checkbox" role="switch" :checked="checksEnabled" :disabled="busy || locked" aria-describedby="release-check-help" @change="emit('update:checksEnabled',($event.target as HTMLInputElement).checked)" /><strong>{{intent==='save-progress'?tr('提交内容检查'):tr('发布前检查')}}</strong></label><span :class="!checksEnabled?'stale':stale?'stale':displayStatus">{{!checksEnabled?tr('未检查'):stale?tr('需重新检查'):displayStatus==='blocked'?tr('待处理'):labels[displayStatus]||displayStatus}}</span></div>
      <p id="release-check-help">{{checksEnabled?tr('检查敏感信息、遗漏文件和已配置的测试，减少误传与发布失败。'):tr('不检查敏感信息、遗漏文件和测试；Git、版本校验及云端检查不受影响。')}}</p>
      <p v-if="checksEnabled && stale">{{tr('文件或发布范围已变化，下次提交时会自动重新检查。')}}</p>
      <p v-if="checksEnabled && candidate?.accepted && !candidate.checksSkipped && !stale && !busy" class="passed">{{cloudBuild?tr('本地检查已通过。GitHub 构建会在发布后执行，结果另行显示。'):tr('发布前检查已通过。')}}</p>
      <ReleaseCheckIssues v-if="checksEnabled && candidate && !candidate.checksSkipped" :app-id="appId" :candidate="candidate" :files="files" :selected="selected" :busy="busy" :stale="stale" :finding-decisions="findingDecisions" :resolving-review="resolvingReview" @choose="(file,included)=>emit('choose',file,included)" @check="emit('check')" @exception="(finding,reason)=>emit('exception',finding,reason)" />
    </div>
  </section>
</template>
<style scoped>
.check-toggle{display:flex;align-items:center;gap:9px;cursor:pointer}.check-toggle input{appearance:none;width:32px;height:18px;border-radius:12px;background:var(--border);position:relative;margin:0;flex-shrink:0;cursor:pointer}.check-toggle input:checked{background:var(--accent)}.check-toggle input:before{content:'';position:absolute;width:12px;height:12px;top:3px;left:3px;border-radius:50%;background:#fff}.check-toggle input:checked:before{left:17px}.check-toggle input:focus-visible{outline:2px solid var(--accent);outline-offset:3px}.check-toggle input:disabled{opacity:.5;cursor:not-allowed}
.release-safety,.release-safety>*{min-width:0}.file-groups{overflow-x:hidden}.scope-file label,.scope-file code{min-width:0}
.file-row{display:flex;align-items:center;gap:8px}.scope-file .file-name{flex:1;min-width:0;text-align:left;background:none;border:0;padding:3px 0;margin:0;color:var(--text);font-size:12px}.scope-file .file-name:hover{color:var(--accent)}.scope-file .file-name code{display:block;overflow-wrap:anywhere;white-space:normal}.file-row>input{flex-shrink:0}
.category-counts>button{padding:6px 9px;border-radius:6px;font-size:12px}.category-counts>button[aria-pressed=true]{border-color:var(--accent);background:var(--bg)}
.file-toolbar{display:flex;gap:8px;flex-wrap:wrap}.file-toolbar input{flex:1;min-width:120px;width:0}.file-toolbar button{font-size:12px;padding:6px 9px}.review-row{display:flex;align-items:center;gap:12px;font-size:12px}.review-row select{flex:none;width:112px;padding:7px;font:inherit}.scope-file .review-row code{white-space:normal;overflow-wrap:anywhere}.empty-files{font-size:12px;color:var(--text-faint);text-align:center;padding:12px}
.release-safety{border:1px solid var(--border);border-radius:11px;padding:14px;display:grid;gap:12px}.safety-head{display:flex;align-items:center;justify-content:space-between;gap:12px}.safety-head h3{margin:0;font-size:15px}.safety-head span,small{font-size:11px;color:var(--text-faint)}.category-counts{display:flex;flex-wrap:wrap;gap:8px}.category-counts>span{padding:6px 9px;border-radius:6px;background:var(--bg);font-size:12px}.category-counts strong{margin-left:5px}.scope-actions{display:flex;gap:8px}.scope-actions button,.review-notice button{font-size:12px;padding:6px 9px}.review-notice{display:flex;justify-content:space-between;align-items:center;gap:10px;padding:9px;border-radius:7px;background:rgba(251,191,36,.08);color:var(--amber);font-size:12px}.advanced-files summary,.file-groups summary{cursor:pointer;padding:7px 0;font-size:12px}.advanced-files>input{width:100%;margin:8px 0}.file-groups{max-height:320px;overflow:auto}.scope-file{padding:9px 5px;border-top:1px solid var(--border)}.scope-file label{display:flex;align-items:center;gap:8px;font-size:12px}.scope-file code{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.scope-file p,.candidate-result p{margin:5px 0;font-size:11px;color:var(--text-faint);line-height:1.6}.scope-file p{margin-left:24px}.scope-file button{margin-left:24px;font-size:11px}.candidate-result{border-top:1px solid var(--border);padding-top:12px;display:grid;gap:8px}.candidate-result .passed,.candidate-result .ready{color:var(--green)}.candidate-result .failed,.blocked,.sensitive{color:var(--red)}.candidate-result .stale,.review,.baseline-note{color:var(--amber)}.finding{padding:8px;border:1px solid var(--border);border-radius:6px;font-size:11px;overflow-wrap:anywhere}.check-result summary{display:flex;justify-content:space-between;gap:10px;font-size:12px;cursor:pointer;padding:7px;background:var(--bg);border-radius:6px}.check-result pre{max-height:200px;overflow:auto;font-size:11px;background:var(--bg);padding:9px;white-space:pre-wrap;overflow-wrap:anywhere}
/* Keep files visible without giving every section another enclosing card. */
.release-safety { border: 0; border-top: 1px solid var(--border); border-radius: 0; padding: 14px 0 0; gap: 8px; }
.file-groups { max-height: 224px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg); scrollbar-gutter: stable; }
.scope-file { padding: 6px 9px; }
.scope-file:first-child { border-top: 0; }
.scope-file .file-name { min-height: 24px; }
.candidate-result { padding-top: 12px; margin-top: 4px; gap: 4px; }
.candidate-result > p { margin: 2px 0; }
.file-name:focus-visible,.file-toolbar button:focus-visible,.category-counts button:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
@media (pointer: coarse) { .scope-file .file-name,.file-toolbar button,.category-counts button { min-height: 44px; } }
</style>
