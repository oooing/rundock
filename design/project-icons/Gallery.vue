<script setup lang="ts">
import { computed, ref } from 'vue'
import UiIcon from '../../src/components/UiIcon.vue'
import DemoCard from './DemoCard.vue'
import { designs, gripDesigns, samples, type PreviewState, type Surface } from './data'

const view = ref(-1)
const handleView = ref(0)
const handleDesign = ref(2)
const handleSampleIds = ref<Record<number, string>>({ 1: 'mixed', 2: 'mixed' })
const pickedHandle = ref(0)
const sampleId = ref('localplay')
const state = ref<PreviewState>('running')
const surface = ref<Surface>('project')
const noIcon = ref(false)
const longName = ref(false)
const picked = ref(0)
const feedback = ref('所有按钮只作演示，不会操作真实项目。')
const sample = computed(() => samples.find(item => item.id === sampleId.value) || samples[0])
const active = computed(() => designs.find(item => item.id === view.value))
const activeHandle = computed(() => gripDesigns.find(item => item.id === handleView.value))
const hasChoice = computed(() => !!(picked.value || pickedHandle.value))
function handleSamples(id: number) {
  return handleSampleIds.value[id] === 'mixed' ? samples.slice(0, 3) : samples.filter(item => item.id === handleSampleIds.value[id])
}
function pick(id: number) {
  picked.value = id
  feedback.value = `已暂选方案 ${id}「${designs[id - 1].name}」。回聊天确认后再实施，正式卡片没有改动。`
}
function pickHandle(id: number) {
  pickedHandle.value = id
  feedback.value = `已暂选把手 ${id}「${gripDesigns[id - 1].name}」，搭配「${designs[handleDesign.value - 1].name}」。只在此页预览，正式卡片没有改动。`
}
function clearChoice() {
  picked.value = 0
  pickedHandle.value = 0
  feedback.value = '已取消暂选。正式卡片没有改动。'
}
</script>

<template>
  <main class="gallery">
    <header class="gallery-header">
      <div class="intro"><div class="brand"><UiIcon name="grid" :size="17" /><span>RunDock 设计预览</span></div><h1>{{ view === -1 ? '把手，放在哪里更顺手？' : '图标，放在哪里更好？' }}</h1><p>{{ view === -1 ? '保留 1 和 2。悬浮有底色，不同项目图标都能试。' : '五种设计，同一套内容。先看效果，再决定用哪一种。' }}</p></div>
      <div class="boundary"><UiIcon name="check-circle" :size="17" /><span>独立 Demo<br /><small>不连接项目，不保存设置</small></span></div>
    </header>
    <div class="view-switch" role="group" aria-label="预览方式">
      <button type="button" :aria-pressed="view === -1" @click="view = -1; handleView = 0"><svg width="16" height="4" viewBox="0 0 16 4" fill="currentColor" aria-hidden="true"><circle cx="3" cy="2" r="1.2" /><circle cx="8" cy="2" r="1.2" /><circle cx="13" cy="2" r="1.2" /></svg>把手对比</button>
      <button type="button" :aria-pressed="view === 0" @click="view = 0"><UiIcon name="grid" :size="15" />五种对比</button>
      <button v-for="design in designs" :key="design.id" type="button" :aria-pressed="view === design.id" @click="view = design.id"><span>{{ design.id }}</span>{{ design.name }}</button>
    </div>
    <section class="controls" aria-label="示例选项">
      <label v-if="view === 0">示例项目<select v-model="sampleId"><option v-for="item in samples" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
      <label v-if="view === -1">卡片设计<select v-model="handleDesign"><option v-for="design in designs" :key="design.id" :value="design.id">{{ design.name }}</option></select></label>
      <label>卡片底色<select v-model="surface"><option value="project">项目原色</option><option value="neutral">深色中性</option><option value="light">浅色卡片</option></select></label>
      <label>项目状态<select v-model="state"><option value="running">运行中</option><option value="stopped">已停止</option><option value="failed">启动失败</option></select></label>
      <div class="checks"><label><input v-model="noIcon" type="checkbox" />没有图标</label><label><input v-model="longName" type="checkbox" />长项目名</label></div>
    </section>

    <section v-if="view === -1 && handleView === 0" class="comparison handle-comparison" aria-label="两种把手位置对比">
      <section v-for="grip in gripDesigns" :key="grip.id" class="handle-option" :aria-labelledby="`handle-${grip.id}`">
        <header class="option-heading"><span class="design-number">{{ grip.id }}</span><div><h2 :id="`handle-${grip.id}`">{{ grip.name }}</h2><p>{{ grip.note }}</p></div><button class="expand-button" type="button" :aria-label="`查看把手 ${grip.id} 的整组卡片`" @click="handleView = grip.id"><UiIcon name="external-link" :size="15" />看整组</button></header>
        <label class="handle-filter">项目图标<select v-model="handleSampleIds[grip.id]" :aria-label="`把手 ${grip.id} 的项目图标`"><option value="mixed">多个项目</option><option v-for="item in samples" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
        <div class="handle-samples"><DemoCard v-for="item in handleSamples(grip.id)" :key="item.id" :variant="handleDesign" :grip-position="grip.id" :sample="item" :state="state" :surface="surface" :no-icon="noIcon" :long-name="longName" @feedback="feedback = $event" /></div>
        <p class="design-description">{{ grip.description }}</p>
        <div class="option-footer"><small>{{ grip.tradeoff }}</small><button type="button" class="pick-button" :aria-pressed="pickedHandle === grip.id" @click="pickHandle(grip.id)"><UiIcon v-if="pickedHandle === grip.id" name="check" :size="14" />{{ pickedHandle === grip.id ? '已暂选' : '暂选把手' }}</button></div>
      </section>
    </section>

    <section v-else-if="view === -1 && activeHandle" class="full-preview handle-preview" aria-labelledby="active-handle-title">
      <header class="full-heading"><div><button class="back-button" type="button" @click="handleView = 0">返回两种对比</button><h2 id="active-handle-title">{{ activeHandle.id }} · {{ activeHandle.name }}</h2><p>{{ activeHandle.description }}</p></div><button type="button" class="pick-button" :aria-pressed="pickedHandle === activeHandle.id" @click="pickHandle(activeHandle.id)">{{ pickedHandle === activeHandle.id ? '已暂选' : '暂选把手' }}</button></header>
      <div class="project-grid"><DemoCard v-for="item in samples" :key="item.id" :variant="handleDesign" :grip-position="activeHandle.id" :sample="item" :state="state" :surface="surface" :no-icon="noIcon" :long-name="longName" @feedback="feedback = $event" /></div>
    </section>

    <section v-else-if="view === 0" class="comparison" aria-label="五种设计对比">
      <section v-for="design in designs" :key="design.id" class="design-option" :aria-labelledby="`design-${design.id}`">
        <header class="option-heading"><span class="design-number">{{ design.id }}</span><div><h2 :id="`design-${design.id}`">{{ design.name }}</h2><p>{{ design.note }}</p></div><button class="expand-button" type="button" :aria-label="`查看方案 ${design.id} 的整组卡片`" @click="view = design.id"><UiIcon name="external-link" :size="15" />看整组</button></header>
        <DemoCard :variant="design.id" :sample="sample" :state="state" :surface="surface" :no-icon="noIcon" :long-name="longName" @feedback="feedback = $event" />
        <p class="design-description">{{ design.description }}</p>
        <div class="option-footer"><small>{{ design.tradeoff }}</small><button type="button" class="pick-button" :aria-pressed="picked === design.id" @click="pick(design.id)"><UiIcon v-if="picked === design.id" name="check" :size="14" />{{ picked === design.id ? '已暂选' : '暂选这版' }}</button></div>
      </section>
    </section>

    <section v-else-if="active" class="full-preview" aria-labelledby="active-title">
      <header class="full-heading"><div><h2 id="active-title">{{ active.id }} · {{ active.name }}</h2><p>{{ active.description }}</p></div><button type="button" class="pick-button" :aria-pressed="picked === active.id" @click="pick(active.id)"><UiIcon v-if="picked === active.id" name="check" :size="15" />{{ picked === active.id ? '已暂选这版' : '暂选这版' }}</button></header>
      <div class="project-grid"><DemoCard v-for="item in samples" :key="item.id" :variant="active.id" :sample="item" :state="state" :surface="surface" :no-icon="noIcon" :long-name="longName" @feedback="feedback = $event" /></div>
    </section>
    <footer class="review-footer" :class="{ 'has-choice': hasChoice }"><UiIcon :name="hasChoice ? 'check-circle' : 'help'" :size="18" /><p role="status" aria-live="polite">{{ feedback }}</p><button v-if="hasChoice" type="button" class="clear-choice" @click="clearChoice">取消暂选</button></footer>
  </main>
</template>

<style scoped>
.gallery { max-width: 1500px; margin: 0 auto; padding: 36px 36px 32px; }
.gallery-header { display: flex; align-items: center; justify-content: space-between; gap: 32px; padding-bottom: 30px; }
.brand { display: flex; align-items: center; gap: 9px; color: var(--text-dim); font-size: 13px; margin-bottom: 16px; }
h1 { font-size: clamp(25px, 2.4vw, 34px); font-weight: 650; letter-spacing: -.04em; line-height: 1.3; margin: 0 0 12px; }
.intro p { margin: 0; color: var(--text-dim); line-height: 1.7; }
.boundary { display: flex; gap: 10px; flex-shrink: 0; color: var(--text-dim); font-size: 13px; line-height: 1.7; }.boundary > svg { color: #81b3ed; margin-top: 3px; }.boundary small { font-size: 12px; color: var(--text-faint); }
.view-switch { display: flex; flex-wrap: wrap; gap: 6px; border-bottom: 1px solid var(--border); padding-bottom: 14px; }
.view-switch button { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 38px; padding: 8px 14px; border-color: transparent; background: transparent; color: var(--text-dim); }
.view-switch button[aria-pressed='true'] { background: #23334c; color: #b5d2ff; border-color: #405d86; }.view-switch button > span { font-size: 12px; opacity: .8; }
.controls { display: flex; align-items: center; gap: 18px; flex-wrap: wrap; padding: 20px 0 28px; }
.controls > label { display: flex; gap: 10px; align-items: center; font-size: 12px; color: var(--text-dim); }
.controls select { max-width: 190px; min-height: 34px; font-size: 12px; padding: 6px 28px 6px 10px; background: var(--bg-elev); }
.checks { display: flex; align-items: center; flex-wrap: wrap; gap: 18px; margin-left: auto; }.checks label { display: flex; align-items: center; gap: 7px; color: var(--text-dim); font-size: 12px; cursor: pointer; }.checks input { width: 15px; height: 15px; margin: 0; accent-color: #86b7ff; }
.comparison, .project-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 30px 22px; }
.handle-comparison { grid-template-columns: repeat(2,minmax(0,1fr)); max-width: 1060px; margin-inline: auto; align-items: start; }
.handle-samples { display: grid; gap: 16px; }
.handle-filter { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 16px; color: var(--text-dim); font-size: 12px; }
.handle-filter select { min-width: 0; max-width: 70%; min-height: 34px; padding: 6px 28px 6px 10px; background: var(--bg-elev); font-size: 12px; }
.design-option,.handle-option { min-width: 0; }.option-heading { display: flex; align-items: center; gap: 11px; margin-bottom: 14px; }
.back-button { padding: 0; margin-bottom: 16px; border: 0; background: transparent; color: #9bc1ff; font-size: 12px; }
.design-number { font-size: 24px; line-height: 1; color: #8797b0; font-weight: 400; font-variant-numeric: tabular-nums; }
.option-heading h2 { font-size: 16px; font-weight: 600; margin: 0 0 4px; }.option-heading p { margin: 0; font-size: 11px; color: var(--text-faint); }
.expand-button { margin-left: auto; display: inline-flex; align-items: center; gap: 5px; background: transparent; border-color: transparent; color: var(--text-dim); font-size: 11px; padding: 7px; }
.design-description { margin: 13px 0 8px; font-size: 13px; line-height: 1.7; color: var(--text); }
.option-footer { display: flex; align-items: center; gap: 12px; justify-content: space-between; }.option-footer small { font-size: 11px; color: var(--text-faint); line-height: 1.6; }
.pick-button { display: inline-flex; justify-content: center; align-items: center; gap: 5px; flex-shrink: 0; color: var(--text-dim); border: 1px solid var(--border); background: transparent; min-height: 32px; font-size: 12px; }
.pick-button[aria-pressed='true'] { border-color: #729cdb; color: #b5d2ff; background: #23334c; }
.full-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 24px; }.full-heading h2 { font-size: 22px; margin: 0 0 10px; font-weight: 600; }.full-heading p { margin: 0; color: var(--text-dim); line-height: 1.6; }
.review-footer { display: flex; align-items: center; gap: 10px; min-height: 54px; margin-top: 36px; padding: 16px 0 0; border-top: 1px solid var(--border); color: var(--text-dim); }.review-footer p { margin: 0; flex: 1; font-size: 12px; line-height: 1.7; }.clear-choice { border: 0; background: transparent; color: var(--text-dim); font-size: 12px; }
.review-footer.has-choice { position: fixed; z-index: 10; bottom: 18px; left: 50%; translate: -50% 0; width: min(920px,calc(100% - 32px)); margin: 0; padding: 14px 18px; background: var(--bg-elev); color: var(--text); border: 1px solid #405d86; border-radius: 10px; box-shadow: 0 8px 30px #0005; }.gallery:has(.has-choice) { padding-bottom: 100px; }
.gallery :is(button,select,input):focus-visible { outline: 2px solid #9bc1ff; outline-offset: 3px; }
@media (hover: hover) { .view-switch button:hover, .expand-button:hover, .pick-button:hover, .clear-choice:hover { background: var(--bg-elev-2); color: var(--text); } }
@media (prefers-reduced-motion: no-preference) { .gallery button { transition: background-color 150ms ease-out, scale 150ms ease-out; }.gallery button:active:not(:disabled) { scale: .96; } }
@media (prefers-reduced-motion: reduce) { .gallery button { transition: none; } }
@media (max-width: 1120px) { .gallery { padding: 28px; }.comparison,.project-grid { grid-template-columns: repeat(2,minmax(0,1fr)); }.checks { margin-left: 0; } }
@media (max-width: 730px) { .gallery { padding: 24px 16px; }.gallery-header { align-items: flex-start; gap: 16px; }.boundary { display: none; }.comparison,.project-grid { grid-template-columns: minmax(0,1fr); gap: 30px; }.controls { gap: 12px 16px; }.controls > label { justify-content: space-between; flex: 1 1 240px; }.controls select { width: 180px; max-width: 60%; }.checks { flex-basis: 100%; padding-top: 4px; }.view-switch button { flex: 1 1 135px; padding-inline: 8px; }.full-heading { align-items: flex-start; }.full-heading h2 { font-size: 20px; }.full-heading p { font-size: 13px; }.review-footer { flex-wrap: wrap; }.review-footer p { min-width: 180px; } }
</style>
