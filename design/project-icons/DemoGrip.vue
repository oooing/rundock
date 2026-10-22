<script setup lang="ts">
defineProps<{ vertical?: boolean }>()
</script>

<template>
  <button class="grip icon-button" type="button" aria-label="拖动项目" title="拖动项目（位置演示）">
    <svg class="grip-dots" :width="vertical ? 4 : 16" :height="vertical ? 16 : 4" :viewBox="vertical ? '0 0 4 16' : '0 0 16 4'" fill="currentColor" aria-hidden="true">
      <circle v-for="index in 3" :key="index" :cx="vertical ? 2 : 3 + (index - 1) * 5" :cy="vertical ? 3 + (index - 1) * 5 : 2" r="1.2" />
    </svg>
  </button>
</template>
