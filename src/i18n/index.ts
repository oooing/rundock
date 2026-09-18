import { readonly, ref } from 'vue'
import english from './en.json'

export type Locale = 'zh-CN' | 'en'
export const LOCALE_STORAGE_KEY = 'rundock.ui.locale'
const messages: Record<string, string> = english

export function normalizeLocale(value: unknown): Locale {
  return typeof value === 'string' && /^zh(?:[-_]|$)/i.test(value.trim()) ? 'zh-CN' : 'en'
}

function storedLocale(): Locale | null {
  try {
    const value = window.localStorage.getItem(LOCALE_STORAGE_KEY)
    return value === 'zh-CN' || value === 'en' ? value : null
  } catch {
    return null
  }
}

function browserLocale(): Locale {
  if (typeof navigator === 'undefined') return 'en'
  return normalizeLocale(navigator.language || navigator.languages?.[0])
}

let explicitLocale = storedLocale()
const currentLocale = ref<Locale>(explicitLocale ?? browserLocale())
export const locale = readonly(currentLocale)

/** Resolve the desktop display language before mounting; the browser is a fallback. */
export async function initializeLocale(getSystemLocale: () => Promise<string | null>): Promise<void> {
  if (explicitLocale) return
  try {
    const systemLocale = await getSystemLocale()
    if (!explicitLocale && systemLocale) currentLocale.value = normalizeLocale(systemLocale)
  } catch {
    // Language detection must not prevent startup when the native bridge is unavailable.
  }
}

/** Language is a device preference, separate from project/release configuration. */
export function setLocale(value: unknown): void {
  currentLocale.value = normalizeLocale(value)
  explicitLocale = currentLocale.value
  try {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, currentLocale.value)
  } catch {
    // Private/restricted storage must not prevent switching for this session.
  }
}

export function translate(language: Locale, key: string, values: readonly unknown[] = []): string {
  const message = language === 'en' && Object.prototype.hasOwnProperty.call(messages, key) ? messages[key] : key
  // One pass: user-provided values are neither translated nor interpreted as placeholders.
  return message.replace(/\{(\d+)\}/g, (placeholder, index: string) => {
    const i = Number(index)
    return i < values.length ? String(values[i] ?? '') : placeholder
  })
}

/** Read within a render, computed getter, or action, so visible labels stay reactive. */
export function tr(key: string, values: readonly unknown[] = []): string {
  return translate(currentLocale.value, key, values)
}
