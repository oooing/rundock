<script setup lang="ts">
import { ref, watch } from 'vue'
import UiIcon from './UiIcon.vue'

const props = withDefaults(defineProps<{ src?: string; variant?: 'icon' | 'watermark' }>(), { variant: 'icon' })
const loaded = ref(false)
const failed = ref(false)
watch(() => props.src, () => { loaded.value = false; failed.value = false })
</script>

<template>
  <!-- Decorative: the adjacent project name already labels the card. Reserve
       the same space before/after loading, and never show a broken-image glyph. -->
  <span v-if="variant === 'icon' || (src && !failed)" class="project-logo" :class="{ loaded, 'project-logo-watermark': variant === 'watermark' }" aria-hidden="true">
    <UiIcon v-if="!loaded && variant === 'icon'" name="grid" :size="20" />
    <img v-if="src && !failed" :key="src" :src="src" alt="" width="36" height="36" loading="lazy" decoding="async" draggable="false" @load="loaded = true" @error="failed = true; loaded = false" />
  </span>
</template>

<style scoped>
.project-logo {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 38px;
  width: 38px;
  height: 38px;
  border-radius: 10px;
  color: var(--card-muted, var(--text-dim));
  background: var(--card-panel, var(--bg-elev-2));
}
.project-logo.loaded { background: transparent; }
.project-logo img {
  position: absolute;
  inset: 1px;
  width: 36px;
  height: 36px;
  object-fit: contain;
  opacity: 0;
}
.project-logo.loaded img { opacity: 1; }
.project-logo-watermark, .project-logo-watermark.loaded {
  position: absolute;
  right: -18px;
  bottom: -22px;
  width: 178px;
  height: 178px;
  border-radius: 0;
  background: transparent;
  opacity: .12;
  filter: grayscale(.65) saturate(.6);
  mask-image: linear-gradient(135deg, transparent 8%, #000 85%);
  pointer-events: none;
  user-select: none;
}
.project-logo-watermark img { inset: 0; width: 100%; height: 100%; }
@media (forced-colors: active) { .project-logo-watermark { display: none; } }
</style>
