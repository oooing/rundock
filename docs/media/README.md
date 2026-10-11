# README visual assets

These assets support the Chinese-first and English README pages.

## Product introduction video

The README's first visual is the latest 90-second product introduction, replacing the static hero placement. The original hero files are retained.

- `rundock-intro-90s.mp4`: exact copy of the approved `Product/rundock-intro-90s.mp4`, delivered on 2026-10-11. H.264/AAC, 1920 × 1080, 90 seconds, 17,250,684 bytes, Chinese narration and burned-in subtitles.
- SHA-256: `fde5b89910bf4ed20e23be7567f33cdfc194a283d8d21f1fcab6e4353638fc02`.
- `rundock-intro-poster.jpg`: the matching v2 film poster from `demos/rundock-intro/full-film/output/v2/poster.jpg`.
- Both READMEs embed the GitHub video attachment as a standalone URL for native inline playback, with an explicit download link to the unchanged 1080p original.
- Online version: H.264/AAC, 1280 × 720, 90 seconds, 9,412,321 bytes. Re-encoded from the original with two-pass H.264 at 720 kbit/s, AAC at 96 kbit/s and fast-start metadata to fit GitHub's 10 MB attachment limit. Narration, subtitles and duration are preserved.
- Online attachment: https://github.com/user-attachments/assets/ceb2c598-1ffc-4aa7-9c14-3333052a82fe
- Online SHA-256: `642d6be98a1196c3c627f07135d31e98faaaf73f44b02f44f58a8812171161b4`.
- The film uses the same isolated demo projects described in its production notes, not live personal project data. No new recording, narration, or visual edit was made for this homepage update.

| Asset | Origin |
| --- | --- |
| `hero.zh-CN.png` | AI-generated brand illustration, built-in image generation |
| `hero.en.png` | English text localization of the Chinese hero, built-in image editing |
| `dashboard.png` | Current RunDock dashboard rendered with isolated demo data |
| `release-panel.png` | Current Release tab: target versions, independent version management, files, and notes |
| `release-settings.png` | Current Settings tab: cloud/local radio controls, uploads, configuration file and example buttons |
| `download.zh-CN.svg`, `download.en.svg` | Hand-authored accessible SVG link buttons |

## Optimized display assets

The README loads `.webp` previews and links to the unchanged `.png` originals for full-size viewing. This is an encoding and display-size optimization, not regenerated artwork.

| Preview | Encoding | Size | README width |
| --- | --- | --- | --- |
| `hero.zh-CN.webp` | 1440 × 576, WebP quality 86 | 43,690 bytes | 720 px |
| `hero.en.webp` | 1440 × 576, WebP quality 86 | 45,704 bytes | 720 px |
| `dashboard.webp` | 2220 × 1125, lossless WebP | 59,120 bytes | 720 px |
| `release-panel.webp` | 1380 × 2157, lossless WebP | 59,612 bytes | 640 px |
| `release-settings.webp` | 1380 × 1623, lossless WebP | 42,036 bytes | 640 px, collapsed by default |

UI screenshots recaptured on 2026-09-09 using the actual Vue components. Encoded with Sharp, WebP effort 6. All three screenshot previews were decoded and compared pixel-for-pixel with their PNG originals. Widths are maximum display sizes; images shrink to fit narrower screens. The conceptual hero artwork was kept unchanged.

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
# RunDock brand refresh — v2.0.3

Current README heroes: `hero-rundock.zh-CN.webp` and `hero-rundock.en.webp`, with full-resolution PNG siblings. Created with the built-in imagegen tool. The original Launcher banners are retained as source references.

Edit prompts: replace only the upper-left LAUNCHER wordmark with `RunDock 启动坞` (Chinese) or `RunDock` (English); preserve the lightning icon, navy background, terminal/play/tag tiles, blue connectors, existing headline and subtitle, and wide 2.5:1 composition.

Dashboard and release screenshots were recaptured from the actual RunDock UI with isolated demo data; no real project data or release request was used. WebP heroes are 1440px wide at quality 86; UI screenshots use lossless WebP.
