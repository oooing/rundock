import { readonly, ref } from 'vue'

export type RunningEffect = 'breathe' | 'orbit' | 'chase' | 'ripple' | 'none'
export const MOTION_STORAGE_KEY = 'rundock.ui.runningEffect'
export function normalizeRunningEffect(value: unknown): RunningEffect {
  return value === 'orbit' || value === 'chase' || value === 'ripple' || value === 'none' ? value : 'breathe'
}
function readEffect(): RunningEffect {
  try { return normalizeRunningEffect(window.localStorage.getItem(MOTION_STORAGE_KEY)) }
  catch { return 'breathe' }
}
const effect = ref<RunningEffect>(readEffect())
export const runningEffect = readonly(effect)
export function setRunningEffect(value: unknown) {
  effect.value = normalizeRunningEffect(value)
  try { window.localStorage.setItem(MOTION_STORAGE_KEY, effect.value) }
  catch { /* The choice still applies when storage is unavailable. */ }
}
