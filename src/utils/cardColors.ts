// 卡片背景色工具：只持久化背景色，文字色由前端按背景亮度实时计算。

export const CARD_COLOR_PALETTE = [
  '#1e293b',
  '#312e81',
  '#064e3b',
  '#7c2d12',
  '#78350f',
  '#4c1d95',
  '#0f766e',
  '#1d4ed8',
  '#e0f2fe',
  '#dcfce7',
  '#fef3c7',
  '#fee2e2',
  '#f3e8ff',
  '#e5e7eb',
]

export function normalizeHexColor(color?: string | null): string {
  const c = (color || '').trim().toLowerCase()
  return /^#[0-9a-f]{6}$/.test(c) ? c : ''
}

export function getReadableTextColor(background?: string | null): string {
  const bg = normalizeHexColor(background)
  if (!bg) return ''
  const r = parseInt(bg.slice(1, 3), 16) / 255
  const g = parseInt(bg.slice(3, 5), 16) / 255
  const b = parseInt(bg.slice(5, 7), 16) / 255
  const linear = (v: number) => (v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4))
  const luminance = 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b)
  return luminance < 0.45 ? '#f8fafc' : '#111827'
}

export function pickNextCardColor(usedColors: Array<string | null | undefined>): string {
  const used = new Set(usedColors.map(normalizeHexColor).filter(Boolean))
  for (const color of CARD_COLOR_PALETTE) {
    if (!used.has(color)) return color
  }

  let i = used.size
  while (true) {
    const hue = (i * 137) % 360
    const color = hslToHex(hue, 62, 34)
    if (!used.has(color)) return color
    i++
  }
}

function hslToHex(h: number, s: number, l: number): string {
  s /= 100
  l /= 100
  const k = (n: number) => (n + h / 30) % 12
  const a = s * Math.min(l, 1 - l)
  const f = (n: number) => l - a * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1)))
  const toHex = (x: number) => Math.round(255 * x).toString(16).padStart(2, '0')
  return `#${toHex(f(0))}${toHex(f(8))}${toHex(f(4))}`
}
