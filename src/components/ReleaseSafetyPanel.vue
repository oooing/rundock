<script setup lang="ts">
import { computed, ref } from 'vue'
import { tr } from '@/i18n'
import type { ReleaseCandidate, ReleaseFileCategory, ReleaseFileClassification, ReleaseManualDecision } from '@/types'
const props = defineProps<{intent:'formal'|'save-progress';cloudBuild?:boolean;files: ReleaseFileClassification[]; selected: Record<string,boolean>; decisions: ReleaseManualDecision[]; candidate: ReleaseCandidate|null; busy: boolean; stale: boolean}>()
const emit = defineEmits<{choose:[file:ReleaseFileClassification,included:boolean]; recommend:[]; cancel:[]; refresh:[]; exception:[finding:ReleaseCandidate['sensitiveFindings'][number],reason:string]}>()
const exceptionReasons=ref<Record<string,string>>({})
const search=ref('')
const advancedOpen=ref(false)
const reviewOnly=ref(false)
function reviewDecision(file:ReleaseFileClassification){
  return props.decisions.find(item=>item.path===file.path && item.contentFingerprint===file.contentFingerprint)?.decision
}
function openReview(){search.value='';reviewOnly.value=true;advancedOpen.value=true}
const categories: Array<{id:ReleaseFileCategory;label:string}>= [
  {id:'recommend',label:tr('推荐提交')},{id:'local',label:tr('本地资料')},{id:'review',label:tr('需要确认')},{id:'sensitive',label:tr('敏感阻断')},
]
const groups = computed(() => categories.map(category=> ({...category, files:props.files.filter(file=>file.category===category.id),
  visible:props.files.filter(file=>file.category===category.id && (file.path+' '+file.reasons.join(' ')).toLowerCase().includes(search.value.toLowerCase()))})))
const unresolved = computed(()=>props.files.filter(file=>file.category==='review' && !props.decisions.some(decision=>decision.path===file.path && decision.contentFingerprint===file.contentFingerprint)))
const displayStatus=computed(()=>props.busy?'running':props.candidate?.intent==='formal' && (props.candidate.sensitiveFindings.length || props.candidate.dependencyFindings.some(item=>item.blocked))?'blocked':props.candidate?.status||'pending')
function directories(files:ReleaseFileClassification[]){
  const result=new Map<string,ReleaseFileClassification[]>()
  for(const file of files){const dir=file.group||'.';result.set(dir,[...(result.get(dir)||[]),file])}
  return [...result].map(([path,files])=>({path,files}))
}
const labels:Record<string,string>={pending:tr('提交时自动检查'),running:tr('检查中'),passed:tr('检查通过'),failed:tr('检查失败'),cancelled:tr('已取消'),unverified:tr('未验证'),stale:tr('已失效'),blocked:tr('已阻断'),ready:tr('可以提交'),skipped:tr('已跳过')}
function excludePending(){ for(const file of unresolved.value) emit('choose',file,false) }
</script>
<template>
  <section class="release-safety" :aria-label="tr('发布范围与检查')">
    <div class="safety-head"><h3>{{tr('本次文件范围')}}</h3><span>{{tr('已选')}} {{Object.values(selected).filter(Boolean).length}} / {{files.length}}</span></div>
    <div class="category-counts"><span v-for="group in groups" :key="group.id" :class="group.id">{{group.label}} <strong>{{group.files.length}}</strong></span></div>
    <div v-if="unresolved.length" class="review-notice"><span>{{tr('有 {0} 项用途需要确认。', [unresolved.length])}}</span><div class="scope-actions"><button class="primary" @click="openReview">{{tr('逐项确认')}}</button><button :disabled="busy" @click="excludePending">{{tr('将待确认项留在本地')}}</button></div></div>
    <div class="scope-actions"><button :disabled="busy" @click="emit('recommend')">{{tr('采用推荐范围')}}</button><button :disabled="busy" @click="emit('refresh')">{{tr('刷新文件')}}</button></div>
    <details class="advanced-files" :open="advancedOpen" @toggle="advancedOpen=($event.target as HTMLDetailsElement).open"><summary>{{tr('查看文件 / 高级选择')}}</summary>
      <label class="review-filter"><input v-model="reviewOnly" type="checkbox" />{{tr('只看需要确认的文件')}}</label>
      <input v-model="search" type="search" :placeholder="tr('搜索路径或原因')" :aria-label="tr('搜索路径或原因')" />
      <div class="file-groups"><details v-for="group in groups.filter(group=>group.files.length && (!reviewOnly || group.id==='review'))" :key="group.id" :open="group.id==='review' || !!search">
        <summary>{{group.label}} · {{group.files.length}}</summary>
        <details v-for="directory in directories(group.visible)" :key="directory.path" class="directory-files" :open="reviewOnly || !!search || group.files.length<20"><summary><code>{{directory.path}}</code> · {{directory.files.length}}</summary>
        <div v-for="file in directory.files" :key="file.path" class="scope-file">
          <label><input type="checkbox" :checked="!!selected[file.path]" :disabled="busy || (file.category==='sensitive' && !selected[file.path])" @change="emit('choose',file,($event.target as HTMLInputElement).checked)" /><code :title="file.path">{{file.path}}</code><span>{{file.status}}</span></label>
          <p>{{file.reasons.join('；')}}</p>
          <p v-if="file.baselineKept && !selected[file.path]" class="baseline-note">{{tr('基线中的旧内容仍保留；取消勾选只排除本次改动。')}}</p>
          <div v-if="file.category==='review'" class="review-choices">
            <button :disabled="busy" :aria-pressed="reviewDecision(file)==='include'" @click="emit('choose',file,true)">{{tr('纳入本次提交')}}</button>
            <button :disabled="busy" :aria-pressed="reviewDecision(file)==='exclude'" @click="emit('choose',file,false)">{{tr('留在本地')}}</button>
            <small>{{reviewDecision(file)==='include'?tr('已确认纳入'):reviewDecision(file)==='exclude'?tr('已确认留在本地'):tr('尚未确认')}}</small>
          </div>
        </div>
        </details>
      </details></div>
    </details>
    <div class="candidate-result" aria-live="polite">
      <div class="safety-head"><strong>{{intent==='save-progress'?tr('提交内容检查'):tr('发布前检查')}}</strong><span :class="stale?'stale':displayStatus">{{stale?tr('已失效'):labels[displayStatus]||displayStatus}}</span></div>
      <p>{{intent==='save-progress'?tr('提交时自动检查文件范围和敏感内容，通过后继续提交。'):tr('确认发布后自动检查文件范围、敏感内容和本地依赖，通过后继续发布。')}}</p>
      <p v-if="stale">{{tr('文件或发布范围已变化，下次提交时会自动重新检查。')}}</p>
      <p v-else-if="displayStatus==='blocked'" class="blocked">{{tr('检查未通过，尚未提交或上传。请查看下方具体文件和原因。')}}</p>
      <p v-if="candidate?.accepted && !stale && !busy" class="passed">{{cloudBuild?tr('本地检查已通过。GitHub 构建会在发布后执行，结果另行显示。'):tr('发布前检查已通过。')}}</p>
      <div v-for="finding in candidate?.sensitiveFindings||[]" :key="finding.fingerprint" class="finding blocked"><code>{{finding.path}}<template v-if="finding.line">:{{finding.line}}</template></code> · {{finding.reason}}<details><summary>{{tr('复核疑似误报')}}</summary><p>{{tr('例外只适用于当前文件内容和这一处发现，修改后自动失效。')}}</p><input v-model="exceptionReasons[finding.fingerprint]" :placeholder="tr('记录误报原因，不填写秘密原文')" /><button :disabled="busy || !exceptionReasons[finding.fingerprint]?.trim()" @click="emit('exception',finding,exceptionReasons[finding.fingerprint])">{{tr('记录本处例外并重新检查')}}</button></details></div>
      <div v-for="finding in candidate?.dependencyFindings||[]" :key="`${finding.path}:${finding.reference}`" class="finding" :class="{blocked:finding.blocked}"><code>{{finding.path}}</code> → <code>{{finding.missing}}</code><p>{{finding.reason}}</p><p v-if="finding.suggestion">{{finding.suggestion}}</p></div>
      <details v-for="check in intent==='formal'?candidate?.checkResults||[]:[]" :key="check.id" class="check-result" :open="['failed','unverified'].includes(check.status)"><summary><span>{{check.name}} · {{check.required?tr('必需'):tr('可选')}}</span><b :class="check.status">{{labels[check.status]||check.status}}</b></summary><p v-if="check.reason">{{check.reason}}</p><pre v-if="check.log">{{check.log}}</pre></details>
      <details v-if="candidate?.warnings.length"><summary>{{tr('检查说明')}} · {{candidate.warnings.length}}</summary><p v-for="warning in candidate.warnings" :key="warning">{{warning}}</p></details>
      <small v-if="candidate">{{tr('候选标识')}} <code>{{candidate.id}}</code></small>
      <div class="scope-actions"><button v-if="busy" @click="emit('cancel')">{{tr('取消检查')}}</button></div>
    </div>
  </section>
</template>
<style scoped>
.release-safety,.release-safety>*{min-width:0}.file-groups{overflow-x:hidden}.scope-file label,.scope-file code{min-width:0}.directory-files>summary code{overflow-wrap:anywhere}.review-notice .scope-actions{flex-wrap:wrap}
.review-filter{display:flex;align-items:center;gap:7px;font-size:12px;margin-top:8px}.review-choices{display:flex;align-items:center;flex-wrap:wrap;gap:8px;margin-left:24px}.scope-file .review-choices button{margin-left:0;padding:5px 8px}.review-choices button[aria-pressed=true]{border-color:var(--accent);color:var(--accent);background:var(--bg)}.review-notice{flex-wrap:wrap}
.release-safety{border:1px solid var(--border);border-radius:11px;padding:14px;display:grid;gap:12px}.safety-head{display:flex;align-items:center;justify-content:space-between;gap:12px}.safety-head h3{margin:0;font-size:15px}.safety-head span,small{font-size:11px;color:var(--text-faint)}.category-counts{display:flex;flex-wrap:wrap;gap:8px}.category-counts>span{padding:6px 9px;border-radius:6px;background:var(--bg);font-size:12px}.category-counts strong{margin-left:5px}.scope-actions{display:flex;gap:8px}.scope-actions button,.review-notice button{font-size:12px;padding:6px 9px}.review-notice{display:flex;justify-content:space-between;align-items:center;gap:10px;padding:9px;border-radius:7px;background:rgba(251,191,36,.08);color:var(--amber);font-size:12px}.advanced-files summary,.file-groups summary{cursor:pointer;padding:7px 0;font-size:12px}.advanced-files>input{width:100%;margin:8px 0}.file-groups{max-height:320px;overflow:auto}.scope-file{padding:9px 5px;border-top:1px solid var(--border)}.scope-file label{display:flex;align-items:center;gap:8px;font-size:12px}.scope-file code{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.scope-file p,.candidate-result p{margin:5px 0;font-size:11px;color:var(--text-faint);line-height:1.6}.scope-file p{margin-left:24px}.scope-file button{margin-left:24px;font-size:11px}.candidate-result{border-top:1px solid var(--border);padding-top:12px;display:grid;gap:8px}.candidate-result .passed,.candidate-result .ready{color:var(--green)}.candidate-result .failed,.blocked,.sensitive{color:var(--red)}.candidate-result .stale,.review,.baseline-note{color:var(--amber)}.finding{padding:8px;border:1px solid var(--border);border-radius:6px;font-size:11px;overflow-wrap:anywhere}.check-result summary{display:flex;justify-content:space-between;gap:10px;font-size:12px;cursor:pointer;padding:7px;background:var(--bg);border-radius:6px}.check-result pre{max-height:200px;overflow:auto;font-size:11px;background:var(--bg);padding:9px;white-space:pre-wrap;overflow-wrap:anywhere}
</style>
