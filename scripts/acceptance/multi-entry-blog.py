"""Real Chrome + isolated RunDock database; real blog scripts, no mock API.
Run with --start-blog to additionally start the local blog and check both pages.
Requires the isolated backend at 18751 and Vite at 18752.
"""
import json, sys, urllib.request, time
from pathlib import Path
from playwright.sync_api import sync_playwright, expect

sys.stdout.reconfigure(encoding='utf-8')
root = Path(__file__).resolve().parents[2]
out = root / 'outputs/acceptance/multi-entry-blog-20261103'
out.mkdir(parents=True, exist_ok=True)
blog = Path(r'D:\001oooing\WorkPitt\001AIcoding\053oooing-blog')
base, origin = 'http://127.0.0.1:18751', 'http://127.0.0.1:18752'
report = {'passed': False, 'browser': 'Chrome', 'checks': []}
def api(path):
    with urllib.request.urlopen(base + path, timeout=10) as response:
        return json.load(response)
def passed(text):
    report['checks'].append(text); print(text, flush=True)

with sync_playwright() as pw:
    browser = pw.chromium.launch(channel='chrome', headless=True)
    context = browser.new_context(viewport={'width': 1440, 'height': 1000}, locale='zh-CN')
    page = context.new_page()
    errors, mutations = [], []
    page.on('pageerror', lambda e: errors.append(str(e)))
    page.on('request', lambda r: mutations.append(r.url) if r.method == 'POST' and (r.url.endswith('/start') or r.url.endswith('/stop')) else None)
    page.set_default_timeout(15000)
    def import_path(path):
        page.get_by_role('button', name='添加项目', exact=True).first.click()
        page.locator('#project-source-path').fill(str(path))
        page.locator('#import-app-name').wait_for()
        return page.get_by_role('dialog', name='添加项目')
    try:
        assert api('/api/apps') == [], 'Use a fresh isolated acceptance database'
        page.goto(origin)
        dialog = import_path(blog)
        expect(dialog.locator('input[name="startup-option"]:checked')).to_have_value(str(blog / '启动博客.cmd'))
        expect(dialog.get_by_text('与「启动博客.cmd」内容相同', exact=True)).to_be_visible()
        expect(dialog.get_by_label('入口地址 1', exact=True)).to_have_value('http://127.0.0.1:17653/')
        expect(dialog.get_by_label('入口地址 2', exact=True)).to_have_value('http://127.0.0.1:17653/admin')
        page.screenshot(path=str(out / '01-import.png'), full_page=True)
        passed('Chinese startup recommended; duplicate CMD marked; homepage and admin suggested')
        dialog.locator('#import-app-name').fill('oooing 博客（多入口验收）')
        dialog.get_by_role('button', name='确认添加', exact=True).click()
        expect(dialog).not_to_be_visible()
        expect(page.locator('.card')).to_have_count(1)
        assert len(api('/api/apps')[0]['accessLinks']) == 2
        assert mutations == [], 'Import must not start or stop services'
        passed('One card persisted with two access links; import did not run a script')
        dialog = import_path(blog / '启动博客Demo.cmd')
        expect(dialog.get_by_text('与已添加的「oooing 博客（多入口验收）」脚本内容相同', exact=True)).to_be_visible()
        expect(dialog.get_by_role('button', name='确认添加', exact=True)).to_be_disabled()
        dialog.get_by_label('仍作为独立项目添加').check()
        expect(dialog.get_by_role('button', name='确认添加', exact=True)).to_be_enabled()
        page.screenshot(path=str(out / '02-duplicate.png'), full_page=True)
        dialog.get_by_role('button', name='取消', exact=True).click()
        passed('Existing duplicate requires explicit opt-in; cancelling creates no extra card')
        page.get_by_role('button', name='编辑访问入口').click()
        editor = page.get_by_role('dialog', name='访问入口', exact=True)
        editor.get_by_label('入口名称 1', exact=True).fill('博客首页')
        editor.get_by_label('移除入口 2', exact=True).click()
        editor.get_by_role('button', name='保存', exact=True).click()
        expect(editor).not_to_be_visible()
        page.reload()
        expect(page.locator('.card').get_by_role('link', name='博客首页 ↗', exact=True)).to_be_visible()
        assert len(api('/api/apps')[0]['accessLinks']) == 1
        passed('Editing/removing a link survives refresh without changing the startup script')
        dialog = import_path(blog / '管理博客.cmd')
        expect(dialog.get_by_text('与「oooing 博客（多入口验收）」调用相同启动脚本', exact=True)).to_be_visible()
        dialog.get_by_role('button', name='将访问入口合并到已有项目', exact=True).click()
        expect(dialog).not_to_be_visible()
        saved = api('/api/apps')
        assert len(saved) == 1 and len(saved[0]['accessLinks']) == 2
        assert saved[0]['entryScript'] == str(blog / '启动博客.cmd')
        expect(page.locator('.card').get_by_role('link', name='管理后台 ↗', exact=True)).to_be_visible()
        passed('Admin CMD merges links into the original card; startup entry remains unchanged')
        page.get_by_role('button', name='编辑访问入口').click()
        editor.get_by_role('button', name='添加访问入口').click()
        editor.get_by_label('入口名称 3', exact=True).fill('错误地址')
        editor.get_by_label('入口地址 3', exact=True).fill('https://example.com/')
        editor.get_by_role('button', name='保存', exact=True).click()
        expect(editor.get_by_role('alert')).to_contain_text('本机 http(s) 地址')
        editor.get_by_role('button', name='取消', exact=True).click()
        assert len(api('/api/apps')[0]['accessLinks']) == 2
        passed('Invalid non-local address rejected; cancelled edits do not affect saved links')
        page.screenshot(path=str(out / '03-card.png'), full_page=True)
        if '--start-blog' in sys.argv:
            page.locator('.card').get_by_role('button', name='启动', exact=True).click()
            deadline = time.monotonic() + 180
            ready = False
            while time.monotonic() < deadline:
                try:
                    with urllib.request.urlopen('http://127.0.0.1:17653/api/health', timeout=2) as response:
                        ready = json.load(response).get('service') == 'oooing-blog'
                    if ready: break
                except Exception: pass
                time.sleep(1)
            assert ready, 'Blog did not become ready in 180 seconds'
            passed('Blog started from its real CMD through the isolated RunDock Web instance')
            for name, url in [('博客首页 ↗','http://127.0.0.1:17653/'),('管理后台 ↗','http://127.0.0.1:17653/admin')]:
                with page.expect_response(lambda r: r.url.endswith('/open-url') and r.request.method == 'POST') as response:
                    page.locator('.card').get_by_role('link', name=name, exact=True).click()
                result = response.value
                assert result.ok, result.text()
                assert result.json()['opened'] == url
                web = context.new_page(); web.goto(url); web.wait_for_load_state('networkidle')
                assert web.locator('body').inner_text().strip()
                web.screenshot(path=str(out / ('04-home.png' if url.endswith('/') else '05-admin.png')))
                report.setdefault('pages', []).append({'url': web.url, 'title': web.title()})
                web.close()
            passed('Both card links open their exact real local pages; Chrome rendered homepage and admin')
        page.screenshot(path=str(out / '06-final.png'), full_page=True)
        assert errors == [], errors
        report['passed'] = True
    finally:
        report['consoleErrors'] = errors
        (out / 'chrome-report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf8')
        if not report['passed']:
            page.screenshot(path=str(out / 'failure.png'), full_page=True)
            (out / 'failure.txt').write_text(page.locator('body').inner_text(), encoding='utf8')
        browser.close()
