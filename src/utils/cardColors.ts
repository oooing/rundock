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

// All surfaces stay opaque. Recalculate text and controls against the displayed
// background so light custom colors remain readable after being dimmed.
export function getCardVisualStyle(color?: string | null, stopped = false): Record<string, string> {
  const savedBg = normalizeHexColor(color)
  if (!savedBg) return {}
  const channels = [1, 3, 5].map(start => parseInt(savedBg.slice(start, start + 2), 16))
  const gray = channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
  const bg = stopped ? '#' + channels.map(channel => Math.round((channel * 0.8 + gray * 0.2) * 0.62).toString(16).padStart(2, '0')).join('') : savedBg
  const luminance = (hex: string) => {
    const linear = [1, 3, 5].map(start => parseInt(hex.slice(start, start + 2), 16) / 255)
      .map(v => v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4))
    return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722
  }
  const bgLuminance = luminance(bg)
  const contrast = (hex: string) => (Math.max(luminance(hex), bgLuminance) + 0.05) / (Math.min(luminance(hex), bgLuminance) + 0.05)
  let fg = getReadableTextColor(bg)
  if (contrast(fg) < 4.5) fg = contrast('#111827') >= contrast('#f8fafc') ? '#111827' : '#f8fafc'
  if (contrast(fg) < 4.5) fg = bgLuminance > 0.179 ? '#000000' : '#ffffff'
  const readable = (hex: string) => contrast(hex) >= 4.5 ? hex : fg
  const darkText = luminance(fg) < 0.1
  const action = getCardActionColors(bg, darkText)
  const runningGreen = ['#34d399', '#166534', '#052e16', '#dcfce7', '#000500', '#fbfffc'].find(color => contrast(color) >= 4.5)!
  return {
    '--card-bg': bg,
    '--card-fg': fg,
    '--card-muted': readable(darkText ? '#475569' : '#cbd5e1'),
    '--card-panel': darkText ? 'rgba(255, 255, 255, 0.12)' : 'rgba(0, 0, 0, 0.08)',
    '--card-border': darkText ? 'rgba(17, 24, 39, 0.18)' : 'rgba(255, 255, 255, 0.16)',
    '--card-link': fg,
    '--card-action-bg': action.background,
    '--card-action-hover': action.hover,
    '--card-action-fg': action.text,
    '--card-glow': contrast(action.background) >= 3 ? action.background : fg,
    '--card-status-green': readable(darkText ? '#166534' : '#a7f3d0'),
    '--card-running-text': runningGreen,
    '--card-status-amber': readable(darkText ? '#854d0e' : '#fde68a'),
    '--card-status-red': readable(darkText ? '#991b1b' : '#fecaca'),
  }
}

// Keep the card hue in its primary action, with enough contrast for small text.
export function getCardActionColors(background: string, darkText: boolean) {
  const mix = (target: number, amount: number) => '#' + [1, 3, 5].map(start => {
    const channel = parseInt(background.slice(start, start + 2), 16)
    return Math.round(channel * (1 - amount) + target * amount).toString(16).padStart(2, '0')
  }).join('')
  return {
    background: mix(darkText ? 0 : 255, darkText ? 0.6 : 0.55),
    hover: mix(darkText ? 0 : 255, darkText ? 0.68 : 0.65),
    text: darkText ? '#f8fafc' : '#111827',
  }
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
