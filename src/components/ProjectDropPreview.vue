<script setup lang="ts">
import { tr } from '@/i18n'
import UiIcon from './UiIcon.vue'
import './projectImport.css'
</script>

<template>
  <div class="project-import-layer drop-preview-layer" aria-live="polite">
    <section class="project-import-panel project-import-drop" role="status">
      <div class="drop-symbol"><UiIcon name="plus" :size="30" /></div>
      <h2>{{ tr('松开，添加项目') }}</h2>
      <p>{{ tr('启动文件或项目文件夹') }}</p>
      <small>{{ tr('新项目将排在最前面') }}</small>
    </section>
  </div>
</template>

<style scoped>
.drop-preview-layer { pointer-events: none; background: #0005; z-index: 95; }
</style>
