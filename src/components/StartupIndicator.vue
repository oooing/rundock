<script setup lang="ts">
import type { StartingEffect } from '@/stores/motion'
defineProps<{ effect: StartingEffect }>()
</script>

<template>
  <span class="startup-indicator" :data-effect="effect" aria-hidden="true">
    <span v-for="n in effect === 'wave' ? 5 : effect === 'dots' ? 3 : 1" :key="n" :style="{ '--step': n - 1 }"></span>
  </span>
</template>

<style scoped>
.startup-indicator { flex: 0 0 auto; display: inline-flex; align-items: center; justify-content: center; gap: 3px; width: 30px; height: 14px; color: inherit; }
.startup-indicator > span { display: block; background: currentColor; }
[data-effect='sweep'] { position: relative; width: 34px; height: 4px; overflow: hidden; border-radius: 4px; background: color-mix(in srgb, currentColor 20%, transparent); }
[data-effect='sweep'] > span { position: absolute; inset: 0 auto 0 0; width: 45%; border-radius: inherit; animation: startup-sweep 1.5s cubic-bezier(.4,0,.2,1) infinite; }
[data-effect='ring'] { width: 14px; }
[data-effect='ring'] > span { width: 12px; height: 12px; box-sizing: border-box; border: 1.5px solid currentColor; border-right-color: transparent; border-radius: 50%; background: transparent; animation: startup-ring .9s linear infinite; }
[data-effect='dots'] > span { width: 5px; height: 5px; border-radius: 50%; animation: startup-dots 1.2s ease-in-out infinite; animation-delay: calc(var(--step) * .15s); }
[data-effect='wave'] > span { width: 3px; height: 12px; border-radius: 2px; animation: startup-wave 1.1s ease-in-out infinite; animation-delay: calc(var(--step) * .1s); }
[data-effect='none'] { width: 6px; }
[data-effect='none'] > span { width: 6px; height: 6px; border-radius: 50%; }
@keyframes startup-sweep { 0% { transform: translateX(-110%); } 100% { transform: translateX(240%); } }
@keyframes startup-ring { to { transform: rotate(360deg); } }
@keyframes startup-dots { 0%, 70%, 100% { opacity: .35; transform: translateY(0); } 35% { opacity: 1; transform: translateY(-2px); } }
@keyframes startup-wave { 0%, 100% { transform: scaleY(.3); opacity: .45; } 50% { transform: scaleY(1); opacity: 1; } }
@media (prefers-reduced-motion: reduce) {
  .startup-indicator > span { animation: none; transform: none; opacity: 1; }
  [data-effect='sweep'] > span { left: 28%; }
}
</style>
