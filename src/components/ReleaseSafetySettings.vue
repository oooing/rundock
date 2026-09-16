<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { tr } from '@/i18n'
import type { ReleaseConfig, ReleaseFileClassification, ReleaseFileRule, ReleaseCheckProfile } from '@/types'
const props=defineProps<{codeOnly?:boolean;config:ReleaseConfig;files:ReleaseFileClassification[];busy:boolean}>()
const emit=defineEmits<{save:[rules:ReleaseFileRule[],checks:ReleaseCheckProfile[]];dirty:[value:boolean]}>()
const rules=ref<ReleaseFileRule[]>([]), checks=ref<ReleaseCheckProfile[]>([]), dirty=ref(false)
const directories=computed(()=>[...new Set(props.files.map(file=>file.path.includes('/')?file.path.slice(0,file.path.lastIndexOf('/')+1):'').filter(Boolean))].sort())
watch(()=>props.config, config=>{dirty.value=false;emit('dirty',false);rules.value=JSON.parse(JSON.stringify(config.fileRules||[]));checks.value=JSON.parse(JSON.stringify(config.checkProfiles||[]))},{immediate:true})
function edited(){dirty.value=true;emit('dirty',true)}
function save(){emit('save',JSON.parse(JSON.stringify(rules.value)),JSON.parse(JSON.stringify(checks.value)));}
</script>
<template>
  <section class="safety-settings" @input="edited" @change="edited">
    <h3>{{codeOnly?tr('文件规则'):tr('文件规则与高级检查')}}</h3><p>{{tr('按项目保存，不会修改忽略规则、跟踪状态或本机文件。')}}</p>
    <div class="setting-heading"><strong>{{tr('文件规则')}}</strong><button :disabled="busy" @click="rules.push({id:`rule-${Date.now()}`,pattern:'',kind:'review'});edited()">{{tr('添加规则')}}</button></div>
    <div v-for="(rule,index) in rules" :key="rule.id" class="rule-line"><input v-model="rule.pattern" list="release-rule-directories" :placeholder="tr('目录或通配符，例如 src/')" :aria-label="tr('规则路径')" /><select v-model="rule.kind" :aria-label="tr('文件分类')"><option value="recommend">{{tr('推荐提交')}}</option><option value="local">{{tr('本地资料')}}</option><option value="review">{{tr('需要确认')}}</option><option value="sensitive">{{tr('敏感阻断')}}</option></select><input v-model="rule.reason" :placeholder="tr('用途说明')" /><button class="ghost" :aria-label="tr('删除规则')" @click="rules.splice(index,1);edited()">×</button></div>
    <datalist id="release-rule-directories"><option v-for="dir in directories" :key="dir" :value="dir" /></datalist>
    <p v-if="!rules.length">{{tr('未配置规则，使用保守分类；未知文件需要确认。')}}</p>
    <details v-if="!codeOnly" class="advanced-checks"><summary>{{tr('额外检查（高级，可选）')}}</summary>
    <p>{{tr('RunDock 自动检查文件范围、敏感内容和本地依赖。只有需要额外测试时，才添加命令。')}}</p>
    <div class="setting-heading"><strong>{{tr('自定义检查命令')}}</strong><button :disabled="busy" @click="checks.push({id:`check-${Date.now()}`,name:'',command:'',workingDir:'.',timeoutSeconds:600,required:true,os:[],targetKinds:[]});edited()">{{tr('添加检查')}}</button></div>
    <div v-for="(check,index) in checks" :key="check.id" class="check-form"><div class="rule-line"><input v-model="check.name" :placeholder="tr('检查名称')" /><label><input v-model="check.required" type="checkbox" />{{tr('必需')}}</label><button class="ghost" :aria-label="tr('删除检查')" @click="checks.splice(index,1);edited()">×</button></div><label>{{tr('命令')}}<input v-model="check.command" :placeholder="tr('例如 node --test')" /></label><div class="check-options"><label>{{tr('工作目录')}}<input v-model="check.workingDir" /></label><label>{{tr('超时（秒）')}}<input v-model.number="check.timeoutSeconds" type="number" min="1" max="3600" /></label></div><details><summary>{{tr('适用环境与目标')}}</summary><label>{{tr('操作系统，逗号分隔；留空表示全部')}}<input :value="(check.os||[]).join(',')" @input="check.os=($event.target as HTMLInputElement).value.split(',').map(v=>v.trim()).filter(Boolean)" placeholder="windows, linux, darwin" /></label><label>{{tr('目标类型，逗号分隔；留空包含共享代码')}}<input :value="(check.targetKinds||[]).join(',')" @input="check.targetKinds=($event.target as HTMLInputElement).value.split(',').map(v=>v.trim()).filter(Boolean)" placeholder="web, server, desktop" /></label></details></div>
    <p>{{tr('检查会执行项目代码，可能访问网络。只保存已确认的命令，不填入凭据或私有样本路径。')}}</p><p>{{tr('未添加命令不影响发布；已设为必需的额外检查仍须通过。')}}</p>
    </details>
    <button class="primary" :disabled="busy || !dirty" @click="save">{{codeOnly?tr('确认并保存规则'):tr('确认并保存规则与检查')}}</button>
  </section>
</template>
<style scoped>
.advanced-checks>summary{cursor:pointer;font-size:13px;padding:8px 0}.advanced-checks[open]{display:grid;gap:10px}
.safety-settings{margin:16px 0;padding:14px;border:1px solid var(--border);border-radius:10px;display:grid;gap:10px}.safety-settings h3{margin:0;font-size:14px}.safety-settings p{font-size:11px;line-height:1.6;color:var(--text-faint);margin:0}.setting-heading{display:flex;align-items:center;justify-content:space-between;font-size:12px}.setting-heading button,.safety-settings>button{font-size:12px;padding:7px 9px}.rule-line{display:flex;gap:8px;align-items:center}.rule-line input{min-width:0;flex:1}.rule-line select{max-width:120px}.rule-line input[type=checkbox]{width:auto}.check-form{padding:10px;border:1px solid var(--border);border-radius:8px;display:grid;gap:8px}.check-form label{display:flex;align-items:center;gap:8px;font-size:11px}.check-form label input{min-width:0;flex:1}.check-options{display:grid;grid-template-columns:1fr 1fr;gap:10px}.check-form summary{font-size:11px;cursor:pointer}.check-form details label{margin-top:7px}
</style>
