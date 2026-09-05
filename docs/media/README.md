# README visual assets

These assets support the Chinese-first and English README pages.

| Asset | Origin |
| --- | --- |
| `hero.zh-CN.png` | AI-generated brand illustration, built-in image generation |
| `hero.en.png` | English text localization of the Chinese hero, built-in image editing |
| `dashboard.png` | Actual Launcher UI rendered with isolated demo data |
| `release-panel.png` | Actual Launcher UI rendered with isolated demo release configuration |
| `download.zh-CN.svg`, `download.en.svg` | Hand-authored accessible SVG link buttons |

The heroes are conceptual brand illustrations, not application screenshots. Screenshots use the real Vue components and styles. Only data is substituted: project names, paths, ports, Git status, release targets, and notes are fictional examples. API and WebSocket calls are intercepted in the capture browser; no real repository is submitted or published. The screenshot interface is Chinese in both README languages.

For new captures, use a separate browser context with demo fixtures; never publish personal project paths, credentials, or logs. Preserve readable alternative text and captions in both README files.

## Chinese hero prompt

Mode: built-in image generation. Use case: `ads-marketing`.

```text
Use case: ads-marketing
Asset type: premium wide GitHub README hero banner for an existing Windows desktop project manager named Launcher.
Primary request: visually exquisite, understated, commercial software branding that makes project start/stop control, live logs and Git releases immediately understandable.
Canvas: wide landscape 2.5:1 aspect ratio, approximately 1600 by 640; edge-to-edge solid deep graphite navy (#0b101a) background, refined dark blue light falloff, no white margin.
Composition: left 55% is an expertly typeset editorial headline block with generous 70px safe margins. Top-left a small crisp orange lightning bolt (the app's existing visual motif) followed by the word LAUNCHER in ivory, spaced uppercase. Large immaculate simplified-Chinese sans-serif headline with two lines: "项目再多，" and "也井然有序。" Below it one restrained readable line "启停 · 日志 · Git 发布". Right 45% shows a sculptural, precisely aligned cluster of three matte graphite, softly beveled software-module tiles, connected by a thin electric-blue trace with a couple of turquoise status lights: one terminal glyph >_, one play triangle, one Git tag outline. The main play tile has a subtle blue glass face and delicate orange edge accent echoing the lightning motif. The tiles are a conceptual illustration, NOT a fake application screenshot. Studio-quality 3D materials, realistic soft ambient occlusion, elegant restrained highlights, no excessive glossy neon, no space imagery, no rocket.
Typography: beautifully balanced, highly legible, ivory headline, muted slate secondary text; no tiny copy; accurate Chinese characters and punctuation, render the specified text exactly once.
Mood: calm control, organized workspace, professional developer tool, premium product launch. No claims of user counts, no reviews, no pricing, no watermarks, no extra text or unrelated symbols.
```

## English localization prompt

Mode: built-in image editing. Input: `hero.zh-CN.png`.

```text
Use case: text-localization. Edit target: the supplied wide Launcher README hero. Change ONLY the Chinese text to English, preserving the exact dimensions, background, lighting, orange lightning logo, LAUNCHER label, layout, 3D terminal/play/tag tiles and blue connectors. Headline at left must read exactly two lines: "Every project." and "Under control." in large elegant ivory sans-serif. Subtitle must read exactly "Launch · Logs · Git releases" in restrained readable slate. Keep generous margins, left-text/right-art composition, dark navy premium visual identity. No other text or changes.
```
