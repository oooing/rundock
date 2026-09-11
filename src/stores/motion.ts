import { readonly, ref } from 'vue'

export type StartingEffect = 'sweep' | 'ring' | 'dots' | 'wave' | 'none'
export const STARTING_MOTION_STORAGE_KEY = 'rundock.ui.startingEffect'
export function normalizeStartingEffect(value: unknown): StartingEffect {
  return value === 'ring' || value === 'dots' || value === 'wave' || value === 'none' ? value : 'sweep'
}
function readStartingEffect(): StartingEffect {
  try { return normalizeStartingEffect(window.localStorage.getItem(STARTING_MOTION_STORAGE_KEY)) }
  catch { return 'sweep' }
}
const starting = ref<StartingEffect>(readStartingEffect())
export const startingEffect = readonly(starting)
export function setStartingEffect(value: unknown) {
  starting.value = normalizeStartingEffect(value)
  try { window.localStorage.setItem(STARTING_MOTION_STORAGE_KEY, starting.value) }
  catch { /* The choice still applies when storage is unavailable. */ }
}

export type RunningEffect = 'breathe' | 'orbit' | 'chase' | 'ripple' | 'sheen' | 'aurora' | 'corners' | 'underglow' | 'none'
export const MOTION_STORAGE_KEY = 'rundock.ui.runningEffect'
export function normalizeRunningEffect(value: unknown): RunningEffect {
  return value === 'breathe' || value === 'chase' || value === 'ripple'
    || value === 'sheen' || value === 'aurora' || value === 'corners' || value === 'underglow'
    || value === 'none' ? value : 'orbit'
}
function readEffect(): RunningEffect {
  try { return normalizeRunningEffect(window.localStorage.getItem(MOTION_STORAGE_KEY)) }
  catch { return 'orbit' }
}
const effect = ref<RunningEffect>(readEffect())
export const runningEffect = readonly(effect)
export function setRunningEffect(value: unknown) {
  effect.value = normalizeRunningEffect(value)
  try { window.localStorage.setItem(MOTION_STORAGE_KEY, effect.value) }
  catch { /* The choice still applies when storage is unavailable. */ }
}
