import { computed, readonly, ref } from 'vue'

export type CardDesign = 'icon' | 'watermark'
// Keep the saved key: "top-left" now places a vertical grip beside the title logo.
export type CardHandlePosition = 'top-center' | 'top-left'
export const CARD_APPEARANCE_STORAGE_KEY = 'rundock.ui.cardAppearance'

export function normalizeCardDesign(value: unknown): CardDesign {
  return value === 'watermark' ? value : 'icon'
}
export function normalizeCardHandlePosition(value: unknown): CardHandlePosition {
  return value === 'top-left' ? value : 'top-center'
}
function readAppearance() {
  try {
    const saved = JSON.parse(window.localStorage.getItem(CARD_APPEARANCE_STORAGE_KEY) || 'null')
    return { design: normalizeCardDesign(saved?.design), handlePosition: normalizeCardHandlePosition(saved?.handlePosition) }
  } catch {
    return { design: 'icon' as CardDesign, handlePosition: 'top-center' as CardHandlePosition }
  }
}

// Like language and motion, these are device UI preferences, not project data.
const appearance = ref(readAppearance())
const failed = ref(false)
export const cardDesign = computed(() => appearance.value.design)
export const cardHandlePosition = computed(() => appearance.value.handlePosition)
export const cardAppearanceSaveFailed = readonly(failed)
function persist() {
  try {
    window.localStorage.setItem(CARD_APPEARANCE_STORAGE_KEY, JSON.stringify(appearance.value))
    failed.value = false
  } catch { failed.value = true }
}
export function setCardDesign(value: unknown) {
  appearance.value.design = normalizeCardDesign(value)
  persist()
}
export function setCardHandlePosition(value: unknown) {
  appearance.value.handlePosition = normalizeCardHandlePosition(value)
  persist()
}
