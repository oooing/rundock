<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import type { ReleaseCandidate, ReleaseFindingContext } from '@/types'
const props=defineProps<{appId:string;candidateId:string;finding:ReleaseCandidate['sensitiveFindings'][number];busy:boolean;stale:boolean;canExclude:boolean;decision?:'allow'|'exclude'}>()
const emit=defineEmits<{confirm:[];check:[];exclude:[]}>()
const source=ref<ReleaseFindingContext|null>(null)
const error=ref(''),loading=ref(false),editing=ref(false)
const contextLines=computed(()=>{
 const line=Math.max(1,source.value?.line || 1)
 return source.value?.lines.filter(item=>item.number>=line-3 && item.number<=line+3) || []
})
let controller:AbortController|null=null,epoch=0
watch(()=>[props.appId,props.candidateId,props.finding.fingerprint,props.stale],async()=>{
 controller?.abort();const current=++epoch;controller=new AbortController()
 source.value=null;error.value='';loading.value=false
 if(props.stale)return
 loading.value=true
 try {const result=await api.releaseFindingContext(props.appId,props.candidateId,props.finding.fingerprint,false,controller.signal);if(epoch===current)source.value=result}
 catch(reason){if(epoch===current && !controller.signal.aborted)error.value=reason instanceof Error?reason.message:tr('无法读取对应代码，请重新检查')}
 finally {if(epoch===current)loading.value=false}
},{immediate:true})
onBeforeUnmount(()=>{epoch++;controller?.abort()})
</script>
<template>
 <div class="finding-source">
  <p class="finding-location">{{finding.line?tr('第 {0} 行',[finding.line]):tr('整个文件')}} · {{finding.reason}}</p>
  <p v-if="decision" class="decision-status" role="status">✓ {{decision==='allow'?tr('已允许提交'):tr('已排除此文件的本次改动')}}</p>
  <template v-else>
  <p v-if="stale" role="status">{{tr('内容已变化，请重新检查后判断。')}}</p>
  <p v-else-if="loading" role="status">{{tr('加载中…')}}</p>
  <p v-else-if="error" role="status">{{tr(error)}}</p>
  <template v-else-if="source">
   <pre class="source-code" tabindex="0" :aria-label="tr('本次提交的原始代码')"><code><span v-for="line in contextLines" :key="line.number" class="source-line" :class="{highlight:line.number===source.line}"><span class="line-number" aria-hidden="true">{{line.number}}</span><span>{{line.text || ' '}}</span></span></code></pre>
  </template>
   <div class="finding-actions">
    <button type="button" class="primary" :disabled="busy || stale || loading || !source" @click="emit('confirm')">{{tr('是测试数据，允许提交')}}</button>
    <button type="button" :disabled="busy || !canExclude" :title="tr('取消提交不会移除旧版本中已有的内容。')" @click="emit('exclude')">{{tr('本次不提交此文件')}}</button>
    <button type="button" :disabled="busy" :aria-expanded="editing" @click="editing=!editing">{{tr('我要修改')}}</button>
   </div>
  <p v-if="editing" class="edit-help">{{tr('请在编辑器修改上方文件，保存后点“重新检查”。')}}</p>
  <button v-if="editing" type="button" :disabled="busy" @click="emit('check')">{{tr('重新检查')}}</button>
  </template>
 </div>
</template>
<style scoped>
.finding-source{min-width:0}.finding-location{font-size:12px;color:var(--text-dim);margin:8px 0}.source-code{margin:8px 0;background:var(--bg);border:1px solid var(--border);border-radius:6px;padding:8px 0;max-height:320px;overflow:auto;font:12px/1.7 Consolas,monospace;tab-size:2;white-space:pre-wrap;overflow-wrap:anywhere}.source-line{display:flex;padding:0 10px;gap:12px}.line-number{min-width:3ch;text-align:right;color:var(--text-faint);user-select:none;flex-shrink:0}.source-line>span:last-child{min-width:0}.highlight{background:rgba(251,191,36,.14);border-left:2px solid var(--amber);padding-left:8px}.finding-actions{display:flex;flex-wrap:wrap;gap:8px;margin-top:10px}.finding-actions button,.context-more{font-size:12px;padding:7px 10px}.context-more{background:transparent;color:var(--text-dim)}.edit-help{font-size:12px;color:var(--text-dim)}
.finding-actions button { min-height: 32px; }
@media (pointer: coarse) { .finding-actions button { min-height: 44px; } }
</style>
