<script setup lang="ts">
import { computed } from 'vue'
import ReleaseFindingSource from './ReleaseFindingSource.vue'
import { tr } from '@/i18n'
import { groupSensitiveFindings, hasReleaseIssues } from '@/utils/releaseIssues'
import type { ReleaseCandidate, ReleaseFileClassification } from '@/types'
const props = defineProps<{appId:string;candidate:ReleaseCandidate;files:ReleaseFileClassification[];selected:Record<string,boolean>;busy:boolean;stale:boolean;findingDecisions?:Record<string,'allow'|'exclude'>;resolvingReview?:boolean}>()
const emit = defineEmits<{choose:[file:ReleaseFileClassification,included:boolean];check:[];exception:[finding:ReleaseCandidate['sensitiveFindings'][number],reason:string]}>()
const secretGroups=computed(()=>groupSensitiveFindings(props.candidate.sensitiveFindings))
const pendingCount=computed(()=>props.candidate.sensitiveFindings.filter(f=>props.stale || !props.findingDecisions?.[f.fingerprint]).length)
const missing=computed(()=>props.candidate.dependencyFindings.filter(f=>f.blocked))
const warnings=computed(()=>props.candidate.dependencyFindings.filter(f=>!f.blocked))
const failedChecks=computed(()=>props.candidate.checkResults.filter(c=>c.required && ['failed','unverified','blocked'].includes(c.status) &&
  !(c.id==='rundock:secrets' && secretGroups.value.length) && !(c.id==='rundock:dependencies' && missing.value.length)))
function selectedFile(path:string){return props.files.find(file=>file.path===path && props.selected[path])}
function addableFile(path:string){return props.files.find(file=>file.path===path && file.category==='recommend' && !props.selected[path])}
function exclude(path:string){const file=selectedFile(path);if(file && !props.busy)emit('choose',file,false)}
function include(path:string){const file=addableFile(path);if(file && !props.busy && !props.stale)emit('choose',file,true)}
</script>
<template>
  <div class="check-issues">
    <article v-for="group in secretGroups" :key="group.path" class="issue-card">
      <strong>{{tr('发现 {0} 处疑似敏感内容',[group.findings.length])}}</strong>
      <code class="issue-path">{{group.path}}</code>
      <ReleaseFindingSource v-for="finding in group.findings" :key="candidate.id+finding.fingerprint" :app-id="appId" :candidate-id="candidate.id" :finding="finding" :busy="busy" :stale="stale" :can-exclude="!!selectedFile(group.path)" :decision="stale ? undefined : findingDecisions?.[finding.fingerprint]" @confirm="emit('exception',finding,'用户确认：仅为测试数据，不是真实凭据')" @exclude="exclude(group.path)" @check="emit('check')" />
    </article>
    <article v-for="finding in missing" :key="`${finding.path}:${finding.reference}`" class="issue-card">
      <strong>{{tr('缺少配套文件')}}</strong><code class="issue-path">{{finding.missing}}</code>
      <p>{{addableFile(finding.missing)?tr('文件在本机，但未选入本次提交。'):tr('请补齐文件或修正引用后，重新检查。')}}</p>
      <button v-if="addableFile(finding.missing)" :disabled="busy || stale" @click="include(finding.missing)">{{tr('加入本次提交')}}</button>
      <details class="issue-details"><summary>{{tr('查看详情')}}</summary><p>{{finding.path}} → {{finding.reference}}</p><p>{{finding.reason}}</p></details>
    </article>
    <article v-for="check in failedChecks" :key="check.id" class="issue-card">
      <strong>{{check.name}} · {{tr('未通过')}}</strong>
      <p>{{tr('请根据详情修复，完成后重新检查；无需重新发布版本。')}}</p>
      <details class="issue-details"><summary>{{tr('查看详情')}}</summary><p>{{check.reason}}</p><pre v-if="check.log">{{check.log}}</pre></details>
    </article>
    <p v-if="resolvingReview" class="review-progress" role="status">{{tr('已处理全部问题，正在统一验证。')}}</p>
    <div v-else-if="(!pendingCount || stale) && (hasReleaseIssues(candidate) || stale)" class="issue-next">
      <span>{{tr('处理后点“重新检查”，通过后再确认发布。')}}</span>
      <button class="primary" :disabled="busy" @click="emit('check')">{{busy?tr('检查中…'):tr('重新检查')}}</button>
    </div>
    <details v-if="warnings.length" class="issue-details advisory">
      <summary>{{tr('其他提醒（不阻止提交）')}}</summary>
      <p v-for="finding in warnings" :key="`${finding.path}:${finding.reference}`">{{finding.path}} → {{finding.reference}}<br />{{finding.reason}}</p>
    </details>
    <details class="issue-details diagnostics"><summary>{{tr('检查详情')}}</summary>
      <p v-for="warning in candidate.warnings" :key="warning">{{warning}}</p>
      <div v-for="check in candidate.checkResults" :key="check.id"><p>{{check.name}} · {{check.status}}</p><p>{{check.reason}}</p><pre v-if="check.log">{{check.log}}</pre></div>
      <small>{{tr('候选标识')}} {{candidate.id}}</small>
    </details>
  </div>
</template>
<style scoped>
.check-issues{display:grid;gap:10px;min-width:0}.issue-heading{color:var(--amber);margin:0;font-size:13px}.issue-card{border:1px solid var(--border);border-radius:8px;padding:12px;min-width:0}.issue-card strong{font-size:13px}.issue-path{display:block;margin:8px 0;overflow-wrap:anywhere;font-size:12px}.check-issues p{font-size:12px;line-height:1.6;color:var(--text-dim);overflow-wrap:anywhere}.issue-card button,.issue-next button{font-size:12px;padding:7px 10px}.issue-details{font-size:12px;color:var(--text-dim);margin-top:10px}.issue-details summary{cursor:pointer}.issue-detail{border-top:1px solid var(--border);padding-top:6px;margin-top:8px}.issue-detail input{width:100%;margin-bottom:8px;box-sizing:border-box}.issue-next{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;font-size:12px}.issue-details pre{max-height:200px;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere}.diagnostics small{overflow-wrap:anywhere}
</style>
