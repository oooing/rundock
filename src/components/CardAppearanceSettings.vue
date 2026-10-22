<script setup lang="ts">
import { tr } from '@/i18n'
import { cardDesign, cardHandlePosition, cardAppearanceSaveFailed, setCardDesign, setCardHandlePosition } from '@/stores/cardAppearance'
import UiIcon from './UiIcon.vue'

const designs = ['icon', 'watermark'] as const
const handles = ['top-center', 'top-left'] as const
</script>

<template>
  <section class="card-appearance-settings" aria-labelledby="card-appearance-title">
    <h4 id="card-appearance-title">{{ tr('卡片外观') }}</h4>
    <fieldset>
      <legend>{{ tr('图标样式') }}</legend>
      <div class="appearance-options">
        <label v-for="design in designs" :key="design" class="appearance-option" :class="{ selected: cardDesign === design }">
          <span class="appearance-preview" :class="design" aria-hidden="true"><UiIcon name="grid" :size="20" /><span class="preview-line"></span><span class="preview-line short"></span></span>
          <span class="option-label"><input type="radio" name="card-design" :value="design" :checked="cardDesign === design" @change="setCardDesign(design)" />{{ design === 'icon' ? tr('标题图标') : tr('右下水印') }}</span>
          <small>{{ design === 'icon' ? tr('图标放在项目名称旁。') : tr('图标淡入右下角，不遮挡内容。') }}</small>
        </label>
      </div>
    </fieldset>
    <fieldset>
      <legend>{{ tr('把手位置') }}</legend>
      <div class="appearance-options">
        <label v-for="position in handles" :key="position" class="appearance-option handle-option" :class="{ selected: cardHandlePosition === position }">
          <span class="appearance-preview handle-preview" :class="position" aria-hidden="true"><svg :width="position === 'top-left' ? 4 : 16" :height="position === 'top-left' ? 16 : 4" :viewBox="position === 'top-left' ? '0 0 4 16' : '0 0 16 4'" fill="currentColor"><circle v-for="n in 3" :key="n" :cx="position === 'top-left' ? 2 : 3 + (n - 1) * 5" :cy="position === 'top-left' ? 3 + (n - 1) * 5 : 2" r="1.2" /></svg><UiIcon v-if="position === 'top-left'" name="grid" :size="14" /><span class="preview-line"></span><span class="preview-line short"></span></span>
          <span class="option-label"><input type="radio" name="card-handle-position" :value="position" :checked="cardHandlePosition === position" @change="setCardHandlePosition(position)" />{{ position === 'top-center' ? tr('顶部居中') : tr('图标左侧') }}</span>
        </label>
      </div>
    </fieldset>
    <p v-if="cardAppearanceSaveFailed" class="save-warning" role="alert">{{ tr('无法保存设置，当前选择仅本次生效。') }}</p>
    <p v-else class="appearance-hint">{{ tr('立即生效，仅保存在当前设备。') }}</p>
  </section>
</template>

<style scoped>
h4 { margin: 0 0 12px; font-size: 14px; }
fieldset { min-width: 0; padding: 0; margin: 0 0 16px; border: 0; }
legend { margin-bottom: 10px; color: var(--text-dim); font-size: 13px; }
.appearance-options { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.appearance-option { min-width: 0; padding: 14px 10px 12px; border: 1px solid var(--border); border-radius: 9px; background: var(--bg); cursor: pointer; }
.appearance-option.selected { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 12%, var(--bg)); }
.appearance-option:has(input:focus-visible) { outline: 2px solid var(--accent); outline-offset: 2px; }
.appearance-preview { position: relative; overflow: hidden; display: flex; flex-direction: column; justify-content: center; gap: 8px; width: min(96px, 100%); height: 54px; padding: 12px; margin: 0 auto 12px; border: 1px solid var(--border); border-radius: 6px; color: #a9c3ef; background: #1e293b; }
.appearance-preview > .ui-icon { position: absolute; left: 10px; top: 10px; }
.preview-line { display: block; height: 3px; width: 42px; border-radius: 2px; background: #cbd5e1; opacity: .6; }
.preview-line.short { width: 28px; opacity: .3; }
.icon .preview-line { margin-left: 25px; width: 32px; }.icon .preview-line.short { width: 22px; }
.watermark > .ui-icon { top: auto; left: auto; right: -6px; bottom: -8px; width: 48px; height: 48px; opacity: .22; mask-image: linear-gradient(135deg, transparent 8%, #000 85%); }
.handle-preview { padding-top: 19px; }.handle-preview > svg { position: absolute; top: 5px; left: calc(50% - 8px); }
.handle-preview.top-left > svg { left: 6px; top: 20px; }
.handle-preview.top-left > .ui-icon { left: 17px; top: 21px; }
.handle-preview.top-left .preview-line { margin-left: 24px; width: 26px; }
.handle-preview.top-left .preview-line.short { width: 18px; }
.option-label { display: flex; align-items: center; justify-content: center; gap: 6px; font-size: 12px; }
input { margin: 0; accent-color: var(--accent); }
small { display: block; margin-top: 8px; color: var(--text-dim); font-size: 11px; text-align: center; line-height: 1.6; }
.appearance-hint, .save-warning { margin: 0; font-size: 12px; line-height: 1.6; color: var(--text-dim); }.save-warning { color: var(--red); }
@media (hover: hover) { .appearance-option:not(.selected):hover { background: var(--bg-elev-2); border-color: var(--text-faint); } }
</style>
