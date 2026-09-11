<script setup lang="ts">
import { tr } from '@/i18n'
import { runningEffect, setRunningEffect, startingEffect, setStartingEffect, type RunningEffect, type StartingEffect } from '@/stores/motion'
import { getCardVisualStyle } from '@/utils/cardColors'
import StartupIndicator from './StartupIndicator.vue'

const startingOptions: { value: StartingEffect; label: string }[] = [
  { value: 'sweep', label: '光条推进' },
  { value: 'ring', label: '环形转动' },
  { value: 'dots', label: '三点接力' },
  { value: 'wave', label: '节奏波形' },
  { value: 'none', label: '关闭动效' },
]
const options: { value: RunningEffect; label: string }[] = [
  { value: 'orbit', label: '边缘流光' },
  { value: 'breathe', label: '呼吸光' },
  { value: 'chase', label: '双光追逐' },
  { value: 'ripple', label: '涟漪光晕' },
  { value: 'sheen', label: '镜面掠光' },
  { value: 'aurora', label: '极光漫游' },
  { value: 'corners', label: '星角辉映' },
  { value: 'underglow', label: '柔光托底' },
  { value: 'none', label: '关闭动效' },
]
</script>

<template>
  <section class="motion-settings" aria-labelledby="motion-title">
    <h4 id="motion-title">{{ tr('运动') }}</h4>
    <fieldset class="starting-settings">
      <legend>{{ tr('启动中的显示效果') }}</legend>
      <div class="effect-options">
        <label v-for="option in startingOptions" :key="option.value" class="effect-option" :class="{ selected: startingEffect === option.value }">
          <span class="effect-swatch starting-swatch" :style="getCardVisualStyle('#312e81')" aria-hidden="true">
            <span class="preview-title"></span>
            <span class="preview-status"><StartupIndicator :effect="option.value" /></span>
          </span>
          <span class="option-label"><input type="radio" name="starting-effect" :value="option.value" :checked="startingEffect === option.value" @change="setStartingEffect(option.value)" />{{ tr(option.label) }}</span>
        </label>
      </div>
      <p class="motion-hint">{{ tr('悬停预览，选择即生效并自动保存。') }}</p>
    </fieldset>
    <fieldset>
      <legend>{{ tr('运行中的显示效果') }}</legend>
      <div class="effect-options">
        <label v-for="option in options" :key="option.value" class="effect-option" :class="{ selected: runningEffect === option.value }">
          <span class="effect-swatch card-motion" :data-motion="option.value" :style="getCardVisualStyle('#312e81')" aria-hidden="true"><span></span><span></span></span>
          <span class="option-label"><input type="radio" name="running-effect" :value="option.value" :checked="runningEffect === option.value" @change="setRunningEffect(option.value)" />{{ tr(option.label) }}</span>
        </label>
      </div>
      <p class="motion-hint">{{ tr('悬停预览，选择即生效并自动保存。') }}</p>
    </fieldset>
  </section>
</template>

<style scoped>
h4 { margin: 0 0 12px; font-size: 14px; }
fieldset { margin: 0; padding: 0; border: 0; min-width: 0; }
legend { font-size: 13px; color: var(--text-dim); margin-bottom: 12px; }
.starting-settings { margin-bottom: 20px; }
.effect-swatch.starting-swatch { padding-block: 7px; gap: 4px; }
.effect-swatch.starting-swatch > .preview-title { width: 70%; height: 3px; flex-shrink: 0; background: var(--card-muted); }
.effect-swatch.starting-swatch > .preview-status { display: flex; align-items: center; width: 100%; height: 14px; background: transparent; opacity: 1; color: var(--card-status-amber); }
.motion-hint { margin: 5px 0 0; color: var(--text-dim); font-size: 12px; line-height: 1.6; }
.effect-options { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.effect-option { cursor: pointer; padding: 14px 9px 10px; border: 1px solid var(--border); border-radius: 9px; background: var(--bg); }
.effect-option.selected { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 12%, var(--bg)); }
.effect-option:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
.effect-option:not(.selected):not(:hover):not(:focus-within) .card-motion::before,
.effect-option:not(.selected):not(:hover):not(:focus-within) .card-motion::after,
.effect-option:not(.selected):not(:hover):not(:focus-within) :deep(.startup-indicator > span) { animation-play-state: paused; }
.effect-swatch[data-motion='sheen']::before { animation-delay: -2.8s; }
.effect-swatch[data-motion='ripple']::before,
.effect-swatch[data-motion='ripple']::after { animation-delay: -1.35s; }
.effect-swatch[data-motion='underglow']::before,
.effect-swatch[data-motion='underglow']::after { animation-delay: -2.8s; }
.effect-swatch { display: flex; flex-direction: column; justify-content: center; gap: 6px; width: 64px; height: 39px; padding: 10px; margin: 0 auto 12px; border: 1px solid var(--card-border); border-radius: 6px; background: var(--card-bg); }
.effect-swatch > span { background: var(--card-muted); border-radius: 2px; height: 3px; width: 75%; }
.effect-swatch > span + span { width: 45%; opacity: 0.5; }
.option-label { display: flex; align-items: center; justify-content: center; gap: 5px; font-size: 12px; white-space: nowrap; }
input { margin: 0; accent-color: var(--accent); }
</style>
