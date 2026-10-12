<script setup lang="ts">
defineProps<{ sectionId: string; title: string }>()
</script>

<template>
  <section class="release-form-section" :data-release-section="sectionId" :aria-labelledby="`release-section-${sectionId}`">
    <header class="form-section-head">
      <div class="form-section-copy">
        <h3 :id="`release-section-${sectionId}`">{{ title }}</h3>
      </div>
      <div v-if="$slots.actions" class="form-section-actions"><slot name="actions" /></div>
    </header>
    <div class="form-section-content"><slot /></div>
  </section>
</template>

<style scoped>
.release-form-section { min-width: 0; border: 0; border-radius: 0; background: transparent; }
.release-form-section + .release-form-section { border-top: 1px solid var(--border); padding-top: 22px; }
.form-section-head { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; margin-bottom: 14px; }
.form-section-copy { flex: 1; min-width: 0; }
.form-section-copy h3 { margin: 0; color: var(--text); font-size: 15px; font-weight: 600; line-height: 1.5; }
.form-section-actions { display: flex; align-items: center; gap: 8px; min-width: 0; color: var(--text-faint); font-size: 12px; }
.form-section-content { display: grid; gap: 16px; min-width: 0; padding: 0; }
.form-section-content :deep(.publish-purpose > legend),
.form-section-content :deep(.build-choice > legend),
.form-section-content :deep(.sync-choice legend) { margin-bottom: 8px; font-size: 12px; font-weight: 500; color: var(--text-dim); }
.form-section-content :deep(.sync-choice) { padding: 0; border: 0; }
</style>
