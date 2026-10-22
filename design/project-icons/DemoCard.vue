<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import UiIcon from '../../src/components/UiIcon.vue'
import DemoGrip from './DemoGrip.vue'
import { getCardVisualStyle } from '../../src/utils/cardColors'
import type { Sample, PreviewState, Surface } from './data'

const props = withDefaults(defineProps<{ variant: number; sample: Sample; state: PreviewState; surface: Surface; noIcon: boolean; longName: boolean; gripPosition?: number }>(), { gripPosition: 1 })
const emit = defineEmits<{ (e: 'feedback', message: string): void }>()
const simulated = ref<PreviewState | null>(null)
const broken = ref(false)
watch(() => [props.sample.id, props.state, props.variant], () => { simulated.value = null; broken.value = false })
const currentState = computed(() => simulated.value || props.state)
const running = computed(() => currentState.value === 'running')
const status = computed(() => ({ running: '运行中', stopped: '已停止', failed: '启动失败' })[currentState.value])
const name = computed(() => props.longName ? `${props.sample.name} · 我的长名称项目工作空间` : props.sample.name)
const logo = computed(() => props.noIcon || broken.value ? '' : new URL(`./assets/${props.sample.icon}`, import.meta.url).href)
const style = computed(() => getCardVisualStyle(props.surface === 'neutral' ? '#191c22' : props.surface === 'light' ? '#e5e7eb' : props.sample.color))
function action(label: string) {
  if (label === '启动' || label === '停止') {
    simulated.value = running.value ? 'stopped' : 'running'
    emit('feedback', '只切换了示例状态，没有启动或停止真实项目。')
  } else emit('feedback', `“${label}”是演示按钮，不会操作真实项目。`)
}
</script>

<template>
  <article class="demo-card" :class="[`variant-${variant}`, `state-${currentState}`, `grip-position-${gripPosition}`, { 'light-surface': surface === 'light' }]" :data-grip-position="gripPosition" :style="style" :aria-label="`${name}，${status}`" data-ui>
    <!-- Decorative backgrounds stay behind content and never intercept controls. -->
    <div v-if="logo && variant > 1" class="decoration-plane" aria-hidden="true">
    <div v-if="variant === 2 && logo" class="card-art watermark" aria-hidden="true"><img :src="logo" alt="" draggable="false" @error="broken = true" /></div>
    <div v-if="variant === 3 && logo" class="card-art edge-imprint" aria-hidden="true"><img :src="logo" alt="" draggable="false" @error="broken = true" /></div>
    <div v-if="variant === 4 && logo" class="card-art center-imprint" aria-hidden="true"><img :src="logo" alt="" draggable="false" @error="broken = true" /></div>
    <div v-if="variant === 5 && logo" class="card-art pattern-imprint" aria-hidden="true">
      <div class="pattern-field"><img v-for="tile in 20" :key="tile" :src="logo" alt="" draggable="false" @error="broken = true" /></div>
    </div>
    </div>
    <DemoGrip v-if="gripPosition === 1" @click="action('拖动项目')" />
    <div class="card-content">
      <header class="project-head">
        <div class="name-row">
          <DemoGrip v-if="gripPosition === 2" vertical @click="action('拖动项目')" />
          <span v-if="variant === 1" class="inline-logo" :class="{ fallback: !logo }" aria-hidden="true">
            <img v-if="logo" :src="logo" alt="" draggable="false" @error="broken = true" /><UiIcon v-else name="grid" :size="20" />
          </span>
          <h3 :title="name">{{ name }}</h3>
        </div>
        <div class="identity-row"><span class="status" :class="currentState"><span></span>{{ status }}</span><span class="group">{{ sample.group }}</span></div>
      </header>
      <div class="project-info">
        <div class="service-label"><span>服务地址</span></div>
        <div class="services">
          <div v-for="(port, index) in sample.ports" :key="port" class="service-row"><UiIcon :name="index === 0 ? 'globe' : 'server'" :size="15" /><span class="service-dot" :class="{ alive: running }"></span><span class="service-url">http://localhost:{{ port }}</span><span class="port">:{{ port }}</span></div>
        </div>
        <div class="path-line"><span>启动文件</span><span :title="`D:\\Projects\\${sample.id}\\start.bat`">D:\Projects\{{ sample.id }}\start.bat</span></div>
      </div>
      <footer class="card-actions">
        <div class="run-actions">
          <button class="run-button" type="button" @click="action(running ? '停止' : '启动')"><UiIcon :name="running ? 'square' : 'play'" :size="14" />{{ running ? '停止' : '启动' }}</button>
          <button v-if="running" type="button" class="restart-button" @click="action('重启')"><UiIcon name="refresh" :size="14" />重启</button>
          <button class="release-button" type="button" @click="action('发布')"><UiIcon name="upload" :size="14" />发布</button>
        </div>
        <div class="utility-actions"><button v-for="item in [{ icon: 'log', label: '查看日志' }, { icon: 'external-link', label: '打开网页' }, { icon: 'folder', label: '打开文件夹' }, { icon: 'more-vertical', label: '更多操作' }]" :key="item.icon" type="button" class="icon-button" :aria-label="item.label" :title="item.label" @click="action(item.label)"><UiIcon :name="item.icon" :size="16" /></button></div>
      </footer>
    </div>
  </article>
</template>

<style scoped>
.demo-card { position: relative; isolation: isolate; display: flex; min-width: 0; min-height: 326px; overflow: hidden; border: 1px solid var(--card-border); border-radius: 10px; background: var(--card-bg); color: var(--card-fg); }
.card-content { position: relative; z-index: 1; display: flex; flex: 1; flex-direction: column; gap: 16px; min-width: 0; padding: 18px 18px 12px; }
.project-head { position: relative; display: flex; flex-direction: column; gap: 12px; }
.name-row { display: flex; align-items: center; gap: 10px; min-width: 0; min-height: 38px; }
.name-row h3 { margin: 0; min-width: 0; flex: 1; color: inherit; font-size: 15px; font-weight: 600; line-height: 1.5; overflow-wrap: anywhere; }
.icon-button { display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; width: 28px; height: 28px; padding: 4px; border: 1px solid transparent; color: var(--card-muted); background: transparent; border-radius: 6px; }
.grip { position: absolute; z-index: 2; top: 2px; left: 50%; transform: translateX(-50%); width: 28px; height: 24px; padding: 0; color: var(--card-muted); cursor: grab; }
.grip:active { cursor: grabbing; }
.grip :deep(.grip-dots) { flex-shrink: 0; }
/* Resting handle is transparent; hover gets a flat tint, never a shadow or glow. */
.demo-card .grip, .demo-card .grip:hover:not(:disabled) { background: transparent; border-color: transparent; box-shadow: none; }
/* A slim three-dot mark; invisible hit area stays at least 24 x 24px. */
.grip-position-1 .card-content { padding-top: 28px; }
.grip-position-2 .name-row { gap: 8px; }
.grip-position-2 .name-row > .grip { position: static; transform: none; width: 24px; height: 28px; }
.inline-logo { width: 38px; height: 38px; flex: 0 0 38px; display: grid; place-items: center; border-radius: 10px; }
.inline-logo img { width: 36px; height: 36px; object-fit: contain; }
.inline-logo.fallback { color: var(--card-muted); background: var(--card-panel); }
.identity-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; font-size: 12px; color: var(--card-muted); }
.status { display: inline-flex; align-items: center; gap: 6px; white-space: nowrap; }
.status > span, .service-dot { width: 6px; height: 6px; flex-shrink: 0; background: currentColor; border-radius: 50%; }
.status.running { color: var(--card-running-text); }.status.failed { color: var(--card-status-red); }
.group { font-size: 11px; overflow-wrap: anywhere; }
.project-info { display: flex; flex-direction: column; gap: 8px; color: var(--card-muted); }
.service-label { display: flex; align-items: center; justify-content: space-between; font-size: 11px; }
.services { display: flex; flex-direction: column; gap: 8px; padding: 9px; border-radius: 5px; background: var(--card-panel); }
.service-row { display: flex; align-items: center; gap: 7px; min-width: 0; font-size: 11px; }
.service-dot { width: 5px; height: 5px; }.service-dot.alive { background: var(--card-status-green); }
.service-url { flex: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.port { flex-shrink: 0; font-variant-numeric: tabular-nums; }
.path-line { display: flex; gap: 10px; padding-top: 2px; font-size: 11px; line-height: 1.5; }
.path-line > :first-child { flex-shrink: 0; }.path-line > :last-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.card-actions { position: relative; z-index: 1; margin-top: auto; padding-top: 12px; border-top: 1px solid var(--card-border); display: flex; flex-direction: column; gap: 10px; }
.run-actions { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
.run-actions button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 32px; padding: 6px 10px; font-size: 12px; border-radius: 7px; border: 1px solid var(--card-border); color: var(--card-fg); background: var(--card-panel); }
.run-actions .run-button { background: var(--card-action-bg); color: var(--card-action-fg); border-color: var(--card-action-bg); }
.run-actions .release-button { margin-left: auto; border-color: transparent; background: transparent; color: var(--card-muted); }
.utility-actions { display: flex; gap: 5px; }.utility-actions > :last-child { margin-left: auto; }
.demo-card button:focus-visible { outline: 2px solid var(--card-fg); outline-offset: 3px; }
.state-failed { border-top: 3px solid #ef4444; }
.decoration-plane { position: absolute; z-index: 0; inset: 0; overflow: hidden; pointer-events: none; }
.card-art { position: absolute; z-index: 0; pointer-events: none; user-select: none; }
.card-art img { width: 100%; height: 100%; object-fit: contain; }
/* 2: preserve the original low-contrast bottom-right watermark. */
.watermark { right: -18px; bottom: -22px; width: 178px; height: 178px; opacity: .14; filter: grayscale(.65) saturate(.6); mask-image: linear-gradient(135deg, transparent 8%, #000 85%); -webkit-mask-image: linear-gradient(135deg, transparent 8%, #000 85%); }
.light-surface .watermark { opacity: .09; }
/* 3: one oversized mark, cropped by the edge; its left side fades below copy. */
.edge-imprint { right: -96px; top: 30px; width: 340px; height: 340px; opacity: .17; filter: saturate(.55); mask-image: linear-gradient(90deg, transparent 8%, #000 78%); }
.light-surface .edge-imprint { opacity: .10; }
/* 4: a quiet, centered monochrome seal, with no separate logo container. */
.center-imprint { left: 50%; top: 50%; width: 258px; height: 258px; transform: translate(-50%, -44%); opacity: .075; filter: grayscale(1); mask-image: radial-gradient(ellipse, #000 30%, transparent 72%); }
.light-surface .center-imprint { opacity: .055; }
/* 5: small repeated marks become a subtle diagonal texture, fading near text. */
.pattern-imprint { inset: 0; overflow: hidden; opacity: .085; filter: grayscale(.75); mask-image: linear-gradient(115deg, transparent 12%, #000 80%); }
.pattern-field { position: absolute; right: -58px; top: -78px; display: grid; grid-template-columns: repeat(4, 62px); grid-auto-rows: 62px; gap: 32px; transform: rotate(-18deg); }
.light-surface .pattern-imprint { opacity: .055; }
@media (hover: hover) {
  .demo-card button:not(.grip):hover:not(:disabled) { background: var(--card-panel); border-color: var(--card-muted); }
  .demo-card .grip:hover:not(:disabled) { color: var(--card-fg); background: color-mix(in srgb, var(--card-fg) 14%, var(--card-bg)); }
  .run-actions .run-button:hover { background: var(--card-action-hover); color: var(--card-action-fg); border-color: var(--card-action-hover); }
}
@media (prefers-reduced-motion: no-preference) {
  .demo-card button { transition: background-color 150ms ease-out, scale 150ms ease-out; }
  .demo-card button:active:not(:disabled) { scale: .96; }
}
@media (prefers-reduced-motion: reduce) { .demo-card button { transition: none; } }
@media (max-width: 380px) { .card-content { padding-inline: 14px; } }
@media (forced-colors: active) { .card-art { display: none; }.demo-card { border: 1px solid CanvasText; } }
</style>
