<script setup lang="ts">
import { tr } from '@/i18n'
import { runningEffect, setRunningEffect, type RunningEffect } from '@/stores/motion'
import { getCardVisualStyle } from '@/utils/cardColors'

const options: { value: RunningEffect; label: string }[] = [
  { value: 'breathe', label: '呼吸光' },
  { value: 'orbit', label: '边缘流光' },
  { value: 'chase', label: '双光追逐' },
  { value: 'ripple', label: '涟漪光晕' },
  { value: 'none', label: '关闭动效' },
]
</script>

<template>
  <section class="motion-settings" aria-labelledby="motion-title">
    <h4 id="motion-title">{{ tr('运动') }}</h4>
    <fieldset>
      <legend>{{ tr('运行中的显示效果') }}</legend>
      <div class="effect-options">
        <label v-for="option in options" :key="option.value" class="effect-option" :class="{ selected: runningEffect === option.value }">
          <span class="effect-swatch card-motion" :data-motion="option.value" :style="getCardVisualStyle('#312e81')" aria-hidden="true"><span></span><span></span></span>
          <span class="option-label"><input type="radio" name="running-effect" :value="option.value" :checked="runningEffect === option.value" @change="setRunningEffect(option.value)" />{{ tr(option.label) }}</span>
        </label>
      </div>
    </fieldset>
  </section>
</template>

<style scoped>
h4 { margin: 0 0 12px; font-size: 14px; }
fieldset { margin: 0; padding: 0; border: 0; min-width: 0; }
legend { font-size: 13px; color: var(--text-dim); margin-bottom: 12px; }
.effect-options { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.effect-option { cursor: pointer; padding: 14px 9px 10px; border: 1px solid var(--border); border-radius: 9px; background: var(--bg); }
.effect-option.selected { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 12%, var(--bg)); }
.effect-option:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
.effect-swatch { display: flex; flex-direction: column; justify-content: center; gap: 6px; width: 64px; height: 39px; padding: 10px; margin: 0 auto 12px; border: 1px solid var(--card-border); border-radius: 6px; background: var(--card-bg); }
.effect-swatch > span { background: var(--card-muted); border-radius: 2px; height: 3px; width: 75%; }
.effect-swatch > span + span { width: 45%; opacity: 0.5; }
.option-label { display: flex; align-items: center; justify-content: center; gap: 5px; font-size: 12px; white-space: nowrap; }
input { margin: 0; accent-color: var(--accent); }
</style>
