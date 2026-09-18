<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '@/api/http'
import { tr } from '@/i18n'
import type { ReleaseFilePreview } from '@/types'
const props=defineProps<{appId:string;path:string}>()
const emit=defineEmits<{close:[]}>()
const preview=ref<ReleaseFilePreview|null>(null)
const loading=ref(false)
const error=ref('')
const imageFailed=ref(false)
const container=ref<HTMLElement|null>(null)
function reveal(){container.value?.scrollIntoView({block:'nearest',inline:'nearest'})}
let controller:AbortController|null=null
let generation=0
const size=computed(()=>!preview.value?'':preview.value.size<1024?`${preview.value.size} B`:`${(preview.value.size/1024).toFixed(1)} KB`)
watch(()=>[props.appId,props.path],async()=>{
  controller?.abort()
  const current=++generation
  controller=new AbortController()
  loading.value=true;error.value='';preview.value=null;imageFailed.value=false
  try {
    const result=await api.previewReleaseFile(props.appId,props.path,controller.signal)
    if(current===generation)preview.value=result
  } catch(reason) {
    if(current===generation && !controller?.signal.aborted)error.value=reason instanceof Error?reason.message:tr('无法读取此文件')
  } finally {
    if(current===generation){loading.value=false;await nextTick();if(current===generation)reveal()}
  }
},{immediate:true})
onBeforeUnmount(()=>{generation++;controller?.abort()})
</script>
<template>
  <div ref="container" class="file-preview" role="region" :aria-label="tr('预览文件：{0}',[path])" :aria-busy="loading">
    <div class="preview-head"><span>{{tr('本地文件预览')}}<template v-if="preview"> · {{size}}</template></span><button type="button" @click="emit('close')">{{tr('收起预览')}}</button></div>
    <p v-if="loading" role="status">{{tr('加载中…')}}</p>
    <p v-else-if="error" role="status">{{tr(error)}}</p>
    <template v-else-if="preview">
      <img v-if="preview.kind==='image' && !imageFailed" :src="preview.dataUrl" :alt="path" @load="reveal" @error="imageFailed=true" />
      <p v-else-if="imageFailed">{{tr('图片无法显示，文件可能已损坏')}}</p>
      <template v-else-if="preview.kind==='text'"><pre v-if="preview.text">{{preview.text}}</pre><p v-else>{{tr('空文件')}}</p><small v-if="preview.truncated">{{tr('仅显示前 64 KB')}}</small></template>
      <p v-else>{{tr(preview.message||'此文件仅显示信息，暂不支持内容预览')}}</p>
    </template>
  </div>
</template>
<style scoped>
.file-preview{margin:10px 0 2px;background:var(--bg);border:1px solid var(--border);border-radius:7px;padding:10px;min-width:0}.preview-head{display:flex;align-items:center;justify-content:space-between;gap:8px;font-size:11px;color:var(--text-faint)}.preview-head button{padding:4px 7px;font-size:11px;flex-shrink:0}.file-preview img{display:block;max-width:100%;max-height:220px;object-fit:contain;margin:10px auto}.file-preview pre{max-height:200px;overflow:auto;font-size:12px;line-height:1.6;white-space:pre-wrap;overflow-wrap:anywhere;tab-size:2;margin:10px 0}.file-preview p,.file-preview small{font-size:12px;color:var(--text-dim);overflow-wrap:anywhere}
</style>
