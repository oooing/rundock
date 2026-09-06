import { readonly, ref } from 'vue'
import english from './en.json'

export type Locale = 'zh-CN' | 'en'
export const LOCALE_STORAGE_KEY = 'rundock.ui.locale'
const messages: Record<string, string> = english

export function normalizeLocale(value: unknown): Locale {
  return value === 'en' ? 'en' : 'zh-CN'
}

function storedLocale(): Locale {
  try {
    return normalizeLocale(window.localStorage.getItem(LOCALE_STORAGE_KEY))
  } catch {
    return 'zh-CN'
  }
}

const currentLocale = ref<Locale>(storedLocale())
export const locale = readonly(currentLocale)

/** Language is a device preference, separate from project/release configuration. */
export function setLocale(value: unknown): void {
  currentLocale.value = normalizeLocale(value)
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
