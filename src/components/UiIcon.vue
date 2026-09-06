<script setup lang="ts">
withDefaults(defineProps<{ name: string; size?: number }>(), { size: 16 })

const paths: Record<string, string[]> = {
  grid: ['M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z'],
  folder: ['M3 7V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z'],
  plus: ['M12 5v14 M5 12h14'],
  settings: ['m9 3-1 3-3 1-2 3 2 2-1 3 2 3 3-1 3 2 3-2 3 1 2-3-1-3 2-2-2-3-3-1-1-3H9Z', 'M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z'],
  search: ['M16 10a6 6 0 1 1-12 0 6 6 0 0 1 12 0Z M15 15l6 6'],
  upload: ['M12 16V3 M7 8l5-5 5 5 M4 15v5a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-5'],
  'chevron-down': ['m6 9 6 6 6-6'],
  'external-link': ['M14 3h7v7 M21 3 10 14 M10 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-5'],
  play: ['m7 4 14 8-14 8V4Z'],
  square: ['M5 5h14v14H5z'],
  refresh: ['M20 7v5h-5 M4 17v-5h5 M6 7a7 7 0 0 1 12-1l2 3 M4 15l2 3a7 7 0 0 0 12-1'],
  log: ['M5 3h14v18H5z M9 7h6 M9 11h6 M9 15h4'],
  trash: ['M3 6h18 M9 6V3h6v3 M5 6l1 15h12l1-15 M10 10v7 M14 10v7'],
  edit: ['m15 4 5 5 M4 20l5-1L21 7a2 2 0 0 0-4-4L5 15l-1 5Z'],
  palette: ['M12 3a9 9 0 0 0 0 18h1a2 2 0 0 0 2-2c0-1-1-1-1-2a2 2 0 0 1 2-2h2a3 3 0 0 0 3-3 9 9 0 0 0-9-9Z', 'M7 10h.01 M9 6h.01 M14 6h.01 M18 10h.01'],
  grip: ['M9 5h.01 M15 5h.01 M9 12h.01 M15 12h.01 M9 19h.01 M15 19h.01'],
  server: ['M3 3h18v7H3z M3 14h18v7H3z M7 6.5h.01 M7 17.5h.01 M12 6.5h5 M12 17.5h5'],
  globe: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z M3 12h18 M12 3c-5 5-5 13 0 18 5-5 5-13 0-18Z'],
  database: ['M20 6c0 2-3.6 3-8 3S4 8 4 6s3.6-3 8-3 8 1 8 3Z M4 6v12c0 2 3.6 3 8 3s8-1 8-3V6 M4 12c0 2 3.6 3 8 3s8-1 8-3'],
  help: ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z M9 9a3 3 0 1 1 5 2c-1 1-2 1-2 3 M12 17h.01'],
  close: ['m6 6 12 12 M18 6 6 18'],
  check: ['m5 12 4 4L19 6'],
  'check-circle': ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z M8 12l3 3 5-6'],
  'alert-circle': ['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z M12 7v6 M12 17h.01'],
  'arrow-right': ['M4 12h16 M14 6l6 6-6 6'],
}
</script>

<template>
  <svg class="ui-icon" :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
    <path v-for="(path, index) in paths[name] || paths.help" :key="index" :d="path" />
  </svg>
</template>

<style scoped>
.ui-icon { display: inline-block; flex: 0 0 auto; vertical-align: middle; }
</style>
