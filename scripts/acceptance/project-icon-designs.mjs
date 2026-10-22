// An isolated design review: never connect to the live RunDock backend.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { createServer, build } from 'vite'
import vue from '@vitejs/plugin-vue'

const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const root = process.cwd()
const out = path.join(root, 'outputs/acceptance/project-icon-designs')
fs.mkdirSync(out, { recursive: true })
fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify({ passed: false, status: 'running' }))
const server = await createServer({ configFile: false, root, plugins: [vue()],
  resolve: { alias: { '@': path.join(root, 'src') } },
  optimizeDeps: { entries: ['design/project-icons/index.html'] },
  server: { host: '127.0.0.1', port: 0, watch: { ignored: ['**/src-tauri/**', '**/sidecar/**', '**/.tmp/**', '**/outputs/**', '**/node_modules/**'] } },
})
let browser
try {
  await server.listen()
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  const page = await browser.newPage({ viewport: { width: 1674, height: 956 }, reducedMotion: 'reduce' })
  const origin = new URL(server.resolvedUrls.local[0]).origin
  const errors = [], prohibited = []
  page.on('pageerror', e => errors.push(e.message))
  await page.route('**/*', async route => {
    const url = new URL(route.request().url())
    if (url.origin !== origin || /^\/(api|ws)(\/|$)/.test(url.pathname)) {
      prohibited.push(url.href); await route.abort(); return
    }
    await route.continue()
  })
  await page.goto(origin + '/design/project-icons/')
  await page.getByRole('heading', { name: '把手，放在哪里更顺手？' }).waitFor()
  assert.equal(await page.locator('.handle-option').count(), 2)
  await page.getByRole('group', { name: '预览方式' }).getByRole('button', { name: '五种对比' }).click()
  await page.getByRole('heading', { name: '图标，放在哪里更好？' }).waitFor()
  assert.equal(await page.locator('.design-option').count(), 5)
  const imagesReady = () => page.waitForFunction(() => [...document.images].every(img => img.complete && img.naturalWidth > 0))
  const verifyHandles = async () => {
    const handles = await page.locator('.demo-card').evaluateAll(cards => cards.map(card => {
      const handle = card.querySelector('.grip'), dots = [...handle.querySelectorAll('circle')]
      const bounds = card.getBoundingClientRect(), hit = handle.getBoundingClientRect()
      const glyph = handle.querySelector('svg').getBoundingClientRect()
      const titleTarget = card.querySelector('.inline-logo') || card.querySelector('.name-row h3')
      const titleBounds = titleTarget.getBoundingClientRect()
      const contentStyle = getComputedStyle(card.querySelector('.card-content'))
      const overlaps = [...card.querySelectorAll('.name-row h3, .inline-logo, .identity-row, .service-row, .path-line, .run-actions button, .utility-actions button')].some(el => {
        const rect = el.getBoundingClientRect()
        return Math.min(hit.right, rect.right) - Math.max(hit.left, rect.left) > 1 && Math.min(hit.bottom, rect.bottom) - Math.max(hit.top, rect.top) > 1
      })
      return { position: Number(card.dataset.gripPosition), count: dots.length, columns: new Set(dots.map(dot => dot.getAttribute('cx'))).size,
        rows: new Set(dots.map(dot => dot.getAttribute('cy'))).size,
        centerOffset: Math.abs(hit.x + hit.width / 2 - bounds.x - bounds.width / 2),
        top: hit.y - bounds.y, left: hit.x - bounds.x, bottom: bounds.bottom - hit.bottom,
        within: hit.left >= bounds.left && hit.right <= bounds.right && hit.top >= bounds.top && hit.bottom <= bounds.bottom,
        overlaps, width: hit.width, height: hit.height, glyphWidth: glyph.width, glyphHeight: glyph.height,
        titleGap: titleBounds.x - hit.right, titleCenterOffset: Math.abs(hit.y + hit.height / 2 - titleBounds.y - titleBounds.height / 2),
        topPadding: parseFloat(contentStyle.paddingTop), bottomPadding: parseFloat(contentStyle.paddingBottom),
        leftPadding: parseFloat(contentStyle.paddingLeft), minHeight: parseFloat(getComputedStyle(card).minHeight) }
    }))
    for (const handle of handles) {
      assert.ok([1, 2].includes(handle.position), 'only retained handle positions are rendered')
      const vertical = handle.position === 2
      assert.equal(handle.count, 3); assert.equal(handle.columns, vertical ? 1 : 3); assert.equal(handle.rows, vertical ? 3 : 1)
      assert.equal(handle.glyphWidth, vertical ? 4 : 16); assert.equal(handle.glyphHeight, vertical ? 16 : 4)
      assert.equal(handle.topPadding, vertical ? 18 : 28, 'inline handle needs no separate header strip')
      assert.equal(handle.bottomPadding, 12, 'compact bottom spacing')
      assert.equal(handle.minHeight, 326, 'compact card minimum height')
      if (handle.position === 1) assert.ok(handle.centerOffset < 1 && handle.top >= 2 && handle.top <= 6, 'handle at top center')
      if (vertical) assert.ok(handle.titleGap >= 6 && handle.titleGap <= 10 && handle.titleCenterOffset < 1, 'vertical grip sits immediately left of logo/title and aligns to its center')
      assert.ok(handle.within && !handle.overlaps, `position ${handle.position} stays inside the card without overlapping text or controls`)
      assert.equal(handle.width, vertical ? 24 : 28); assert.equal(handle.height, vertical ? 28 : 24)
    }
  }
  const verifyBackgrounds = async () => {
    const layers = await page.locator('.card-art').evaluateAll(elements => elements.map(el => ({
      hidden: el.getAttribute('aria-hidden'), pointer: getComputedStyle(el).pointerEvents,
      z: Number(getComputedStyle(el).zIndex), contentZ: Number(getComputedStyle(el.closest('.demo-card').querySelector('.card-content')).zIndex),
      opacity: Number(getComputedStyle(el).opacity), mask: getComputedStyle(el).maskImage,
    })))
    for (const layer of layers) {
      assert.equal(layer.hidden, 'true'); assert.equal(layer.pointer, 'none')
      assert.ok(layer.z < layer.contentZ && layer.opacity <= .18)
      assert.notEqual(layer.mask, 'none')
    }
  }
  await imagesReady()
  await verifyHandles(); await verifyBackgrounds()
  assert.equal(await page.locator('.card-art').count(), 4)
  assert.equal(await page.locator('.variant-3 .card-content img, .variant-4 .card-content img, .variant-5 .card-content img').count(), 0, '3-5 only use background logos')
  const watermark = await page.locator('.watermark').evaluate(el => {
    const css = getComputedStyle(el)
    return { opacity: Number(css.opacity), mask: css.maskImage, pointer: css.pointerEvents, width: el.clientWidth }
  })
  assert.ok(watermark.opacity <= .16 && watermark.opacity >= .08)
  assert.match(watermark.mask, /linear-gradient/)
  assert.equal(watermark.pointer, 'none')
  assert.ok(watermark.width >= 150)
  await page.screenshot({ path: path.join(out, 'five-designs.png'), fullPage: true })
  // All action semantics stop at this preview's local state.
  await page.locator('.design-option').first().getByRole('button', { name: '停止', exact: true }).click()
  assert.equal(await page.locator('.status.stopped').count(), 1)
  assert.equal(await page.locator('.status.running').count(), 4)
  assert.match(await page.getByRole('status').innerText(), /没有启动或停止真实项目/)
  await page.locator('.design-option').first().getByRole('button', { name: '发布', exact: true }).click()
  assert.match(await page.getByRole('status').innerText(), /不会操作真实项目/)
  await page.getByLabel('示例项目').selectOption('duck')
  assert.equal(await page.locator('.name-row h3').filter({ hasText: '看剧鸭' }).count(), 5)
  await imagesReady()
  await page.getByLabel('示例项目').selectOption('localplay')

  const names = ['标题图标', '右下水印', '侧边浮影', '居中淡印', '斜向暗纹']
  const marks = ['.inline-logo', '.watermark', '.edge-imprint', '.center-imprint', '.pattern-imprint']
  for (let i = 0; i < names.length; i++) {
    await page.getByRole('group', { name: '预览方式' }).getByRole('button', { name: `${i + 1} ${names[i]}`, exact: true }).click()
    assert.equal(await page.locator('.project-grid .demo-card').count(), 5)
    assert.equal(await page.locator(marks[i]).count(), 5)
    await imagesReady()
    await verifyHandles(); await verifyBackgrounds()
    await page.screenshot({ path: path.join(out, `design-${i + 1}.png`), fullPage: true })
  }

  await page.getByRole('group', { name: '预览方式' }).getByRole('button', { name: '五种对比' }).click()
  await page.locator('.design-option').nth(1).getByRole('button', { name: '暂选这版' }).click()
  assert.match(await page.getByRole('status').innerText(), /暂选方案 2/)
  assert.equal(await page.locator('.pick-button[aria-pressed="true"]').count(), 1)
  assert.equal(await page.evaluate(() => localStorage.length), 0, 'preview must not persist product preferences')
  await page.getByRole('button', { name: '取消暂选' }).click()
  await page.getByLabel('没有图标').check()
  assert.equal(await page.locator('.demo-card img').count(), 0)
  assert.equal(await page.locator('.watermark').count(), 0, 'no giant fallback watermark')
  assert.equal(await page.locator('.card-art').count(), 0, 'missing logos omit every decorative layer')
  await page.getByLabel('长项目名').check()
  await page.getByLabel('卡片底色').selectOption('light')
  await page.getByLabel('项目状态').selectOption('failed')
  assert.equal(await page.locator('.status.failed').count(), 5)
  assert.equal(await page.locator('.demo-card .run-button').filter({ hasText: '启动' }).count(), 5)

  for (const width of [1440, 768, 390, 320]) {
    await page.setViewportSize({ width, height: 900 })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `no page overflow at ${width}`)
    const overflow = await page.locator('.demo-card').evaluateAll(cards => cards.some(card => card.scrollWidth > card.clientWidth + 1))
    assert.equal(overflow, false, `no card overflow at ${width}`)
    await verifyHandles()
    if (width === 390) await page.screenshot({ path: path.join(out, 'mobile-fallback-long-name.png'), fullPage: true })
  }
  await page.getByLabel('没有图标').uncheck()
  await page.getByLabel('长项目名').uncheck()
  await page.getByLabel('卡片底色').selectOption('project')
  await page.getByLabel('项目状态').selectOption('running')
  await imagesReady()
  await page.locator('.design-option').nth(1).screenshot({ path: path.join(out, 'watermark-mobile.png') })
  await verifyHandles(); await verifyBackgrounds()
  await page.screenshot({ path: path.join(out, 'backgrounds-mobile.png'), fullPage: true })
  const firstTab = page.getByRole('group', { name: '预览方式' }).getByRole('button', { name: '1 标题图标', exact: true })
  await firstTab.focus(); await page.keyboard.press('Space')
  assert.equal(await firstTab.getAttribute('aria-pressed'), 'true')
  const transition = await firstTab.evaluate(el => getComputedStyle(el).transitionDuration)
  assert.ok(transition.split(',').every(value => parseFloat(value) <= .00001), 'reduced-motion follows the existing near-zero duration convention')
  // Only the two retained handles remain; each can preview different project icons independently.
  await page.setViewportSize({ width: 1674, height: 956 })
  await page.getByRole('group', { name: '预览方式' }).getByRole('button', { name: '把手对比', exact: true }).click()
  assert.equal(await page.locator('.handle-option').count(), 2)
  assert.deepEqual(await page.locator('.handle-option .demo-card').evaluateAll(cards => cards.map(card => Number(card.dataset.gripPosition))), [1, 1, 1, 2, 2, 2])
  await verifyHandles(); await imagesReady()
  for (const option of await page.locator('.handle-option').all()) {
    assert.equal(await option.locator('.demo-card').count(), 3)
    assert.equal(await option.locator('.demo-card img').evaluateAll(imgs => new Set(imgs.map(img => img.src)).size), 3, 'mixed preview shows different icons without extra clicks')
  }
  await page.screenshot({ path: path.join(out, 'two-handle-positions.png'), fullPage: true })
  const secondTitles = await page.locator('.handle-option').nth(1).locator('h3').allTextContents()
  const selectedIconSources = new Set()
  for (const [id, name] of [['localplay', 'localPlay'], ['rundock', 'RunDock'], ['duck', '看剧鸭'], ['brickmuse', 'BrickMuse · 粒想工坊'], ['photography', 'VirtualPhotography']]) {
    await page.getByLabel('把手 1 的项目图标', { exact: true }).selectOption(id)
    await imagesReady()
    assert.equal(await page.locator('.handle-option').first().locator('h3').innerText(), name)
    const src = await page.locator('.handle-option').first().locator('.demo-card img').getAttribute('src')
    assert.ok(src, 'selected project has a rendered icon')
    assert.ok(!selectedIconSources.has(src), 'each project selection uses a different icon, including inlined SVGs')
    selectedIconSources.add(src)
    assert.deepEqual(await page.locator('.handle-option').nth(1).locator('h3').allTextContents(), secondTitles, 'each handle has its own preview selection')
  }
  await page.getByLabel('把手 2 的项目图标', { exact: true }).selectOption('duck')
  assert.match(await page.locator('.handle-option').nth(1).locator('h3').innerText(), /看剧鸭/)
  assert.match(await page.locator('.handle-option').first().locator('h3').innerText(), /VirtualPhotography/)
  for (const position of [1, 2]) await page.getByLabel(`把手 ${position} 的项目图标`, { exact: true }).selectOption('mixed')
  // A mouse hover gets a clearly visible flat tint, without a shadow or glowing ring.
  for (const surface of ['project', 'light']) {
    await page.getByLabel('卡片底色').selectOption(surface)
    await page.mouse.click(5, 5)
    for (const handle of await page.locator('.handle-option .grip').all()) {
      await page.mouse.move(5, 5)
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)))
      const normal = await handle.evaluate(el => ({ color: getComputedStyle(el).color, background: getComputedStyle(el).backgroundColor }))
      assert.equal(normal.background, 'rgba(0, 0, 0, 0)', 'resting handle has no background')
      await handle.hover()
      const hover = await handle.evaluate(el => {
        const css = getComputedStyle(el), glyph = getComputedStyle(el.querySelector('svg'))
        return { background: css.backgroundColor, border: css.borderTopColor, shadow: css.boxShadow,
          color: css.color, foreground: getComputedStyle(el.closest('.demo-card')).color,
          cardBackground: getComputedStyle(el.closest('.demo-card')).backgroundColor,
          outline: css.outlineStyle, cursor: css.cursor, glyphFilter: glyph.filter }
      })
      assert.notEqual(hover.background, 'rgba(0, 0, 0, 0)', 'hover background is visible')
      assert.notEqual(hover.background, hover.cardBackground, 'hover is distinct from the card')
      assert.equal(hover.border, 'rgba(0, 0, 0, 0)')
      assert.equal(hover.shadow, 'none'); assert.equal(hover.glyphFilter, 'none')
      assert.equal(hover.outline, 'none', 'mouse hover has no ring')
      assert.equal(hover.cursor, 'grab')
      assert.equal(hover.color, hover.foreground); assert.notEqual(hover.color, normal.color)
      await page.mouse.move(5, 5)
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)))
      assert.equal(await handle.evaluate(el => getComputedStyle(el).backgroundColor), 'rgba(0, 0, 0, 0)', 'hover tint clears on pointer leave')
    }
  }
  await page.getByLabel('卡片底色').selectOption('project')
  await page.getByLabel('卡片设计').selectOption('1')
  await imagesReady()
  for (const position of [1, 2]) {
    await page.locator(`.grip-position-${position} .grip`).first().hover()
    await page.locator(`.grip-position-${position}`).first().screenshot({ path: path.join(out, `handle-${position}-hover.png`) })
  }
  await page.getByLabel('卡片设计').selectOption('2')
  const keyboardGrip = page.locator('.handle-option .grip').first()
  await keyboardGrip.focus(); await page.keyboard.press('Tab'); await page.keyboard.press('Shift+Tab')
  const focus = await keyboardGrip.evaluate(el => ({ visible: el.matches(':focus-visible'),
    outline: getComputedStyle(el).outlineStyle, width: parseFloat(getComputedStyle(el).outlineWidth) }))
  assert.ok(focus.visible && focus.outline !== 'none' && focus.width >= 2, 'keyboard focus stays visible')
  for (const position of [1, 2]) {
    await page.getByRole('button', { name: `查看把手 ${position} 的整组卡片`, exact: true }).click()
    assert.equal(await page.locator(`.project-grid .grip-position-${position}`).count(), 5)
    await verifyHandles(); await imagesReady()
    await page.screenshot({ path: path.join(out, `handle-position-${position}.png`), fullPage: true })
    await page.getByRole('button', { name: '返回两种对比', exact: true }).click()
  }
  await page.locator('.handle-option').nth(1).getByRole('button', { name: '暂选把手', exact: true }).click()
  assert.match(await page.getByRole('status').innerText(), /已暂选把手 2「图标左侧」/)
  assert.equal(await page.evaluate(() => localStorage.length), 0)
  await page.getByRole('button', { name: '取消暂选' }).click()
  const demoGrip = page.locator('.handle-option .grip').first()
  await demoGrip.focus(); await page.keyboard.press('Enter')
  assert.match(await page.getByRole('status').innerText(), /拖动项目.*演示按钮/)
  await page.getByLabel('长项目名').check()
  await page.getByLabel('项目状态').selectOption('failed')
  await page.getByLabel('卡片底色').selectOption('light')
  for (const design of [1, 2, 3, 4, 5]) {
    await page.getByLabel('卡片设计').selectOption(String(design))
    for (const width of [1440, 768, 390, 320]) {
      await page.setViewportSize({ width, height: 900 })
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => resolve())))
      await verifyHandles(); await verifyBackgrounds()
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `handles: no page overflow at ${width}`)
      const overflowingCards = await page.locator('.demo-card').evaluateAll(cards => cards.filter(card => card.scrollWidth > card.clientWidth + 1).map(card => ({
        position: card.dataset.gripPosition, client: card.clientWidth, scroll: card.scrollWidth,
        children: [...card.querySelectorAll('.card-content, .name-row, .card-actions, .run-actions, .utility-actions')].map(el => ({ class: el.className, client: el.clientWidth, scroll: el.scrollWidth })),
      })))
      assert.deepEqual(overflowingCards, [], `design ${design}, width ${width}: card overflow`)
    }
  }
  await page.getByLabel('没有图标').check()
  await verifyHandles()
  assert.equal(await page.locator('.card-art').count(), 0)
  await page.screenshot({ path: path.join(out, 'handles-mobile-long-name.png'), fullPage: true })
  assert.deepEqual(errors, [])
  assert.deepEqual(prohibited, [])
  // The demo also bundles independently; it isn't imported by the real app.
  await build({ configFile: false, root, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } },
    build: { outDir: path.join(out, 'build'), emptyOutDir: true, rollupOptions: { input: path.join(root, 'design/project-icons/index.html') } },
  })
  fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify({ passed: true, checks: [
    'five distinct designs and full-project previews', 'existing local icons load', 'masked low-contrast bottom-right watermark',
    '3-5 are distinct background-only logo compositions, behind content with no pointer interception',
    'only positions 1 and 2 remain, with compact horizontal three-dot handles and no overlap',
    'dark and light hover has a flat background tint that clears on leave; no shadow or ring; keyboard focus stays visible',
    'mixed project icons shown by default; independent selectors switch all five project icons',
    '24px minimum hit areas and compact card spacing',
    'both positions across five existing card designs, four widths, missing icons and long names',
    'handle full-group previews, keyboard action feedback and memory-only choice',
    'all actions are demo-only; no backend or external requests', 'selection is memory-only',
    'light surfaces, missing icons, long names, running/stopped/failed states',
    '1440/768/390/320px without overflow', 'keyboard controls and reduced motion', 'independent production bundle',
  ], prohibitedRequests: prohibited, pageErrors: errors }, null, 2))
  console.log('PASS: five icon designs, two handles, hover backgrounds, independent project icons, isolated actions and independent build')
} catch (error) {
  fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify({ passed: false, error: String(error) }, null, 2))
  throw error
} finally { await browser?.close(); await server.close() }
