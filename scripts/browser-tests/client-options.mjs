// Read-only check of the deployed client pickers. No history or official clients are deleted.
import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const root = path.resolve(import.meta.dirname, '../..');
const state = JSON.parse(await fs.readFile(path.join(root, 'runtime/lab-state.json'), 'utf8'));
const base = new URL(state.base_path, state.public_url.replace('localhost', '127.0.0.1')).href;
const report = { passed: false, motion: process.env.CLIENT_OPTIONS_MOTION || 'reduce', checks: [], pageErrors: [] };
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1500, height: 1050 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai', reducedMotion: process.env.CLIENT_OPTIONS_MOTION || 'reduce' });
const page = await context.newPage();
page.on('pageerror', e => report.pageErrors.push(e.message));
async function check(name, fn) { await fn(); report.checks.push(name); console.log('PASS', name); }
try {
  const csrf = (await (await context.request.get(base + 'csrf-token')).json()).obj;
  assert.equal((await (await context.request.post(base + 'login', { form: { username: state.username, password: state.password }, headers: { 'X-CSRF-Token': csrf } })).json()).success, true);
  const clients = (await (await context.request.get(base + 'traffic-monitor/api/v1/clients')).json()).obj;
  report.currentClients = clients.filter(c => !c.deleted).length;
  report.historicalClients = clients.filter(c => c.deleted).length;
  await page.goto(base + 'traffic-monitor/');
  await page.getByText(/本地域名规则集/).waitFor();
  await page.getByRole('switch', { name: '自动刷新实时状态和历史统计' }).click();
  const select = page.getByRole('combobox', { name: '选择客户端' });
  await check('live query picker defaults to current clients', async () => {
    await select.click();
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option').first().waitFor();
    await page.waitForFunction(() => [...document.querySelectorAll('.ant-select-dropdown')].some(node => { const r = node.getBoundingClientRect(); return r.width > 0 && r.x >= 0 && r.y >= 0 && r.y < innerHeight; }));
    assert.equal(await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: '已删除' }).count(), 0);
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-current-clients.png') });
    await select.press('Escape');
  });
  if (report.historicalClients) {
    await check('retained history is explicitly selectable with distinct labels', async () => {
      await page.getByRole('checkbox', { name: '显示历史客户端', exact: true }).check();
      await select.click();
      await select.fill(clients.find(c => c.deleted).email);
      const options = page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: '已删除' });
      await options.first().waitFor();
      const labels = await options.allTextContents();
      assert.equal(new Set(labels).size, labels.length);
      assert.ok(labels.every(label => label.includes(' · #')));
      try { await options.first().click({timeout:5000}); }
      catch (error) {
        report.dropdown = await page.locator('.ant-select-dropdown').evaluateAll(nodes => nodes.map(node => ({className:node.className,style:node.getAttribute('style'),rect:node.getBoundingClientRect().toJSON(),text:node.textContent.slice(0,120)})));
        report.select = await select.evaluate(node=>({expanded:node.getAttribute('aria-expanded'),value:node.value}));
        await page.screenshot({path:path.join(root,'artifacts/screenshots/client-options-diagnostic.png')});
        throw error;
      }
      await page.getByRole('checkbox', { name: '显示历史客户端', exact: true }).uncheck();
      await page.locator('.filter-card .client-picker').getByText('全部客户端', { exact: true }).waitFor();
    });
  }
  await check('deletion picker also defaults to current entries and fits mobile', async () => {
    await page.getByRole('button', { name: '删除日志与流量' }).click();
    const modal = page.getByRole('dialog');
    const picker = modal.getByRole('combobox', { name: '删除哪些客户端' });
    await picker.click();
    assert.equal(await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: '已删除' }).count(), 0);
    await picker.press('Escape');
    await modal.getByRole('button', { name: /取\s*消/ }).click();
    await page.setViewportSize({ width: 390, height: 844 });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 2));
    await page.locator('.filter-card').scrollIntoViewIfNeeded();
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-client-picker-mobile.png') });
  });
  assert.deepEqual(report.pageErrors, []);
  report.passed = true;
} catch (error) {
  report.dropdown = await page.locator('.ant-select-dropdown').evaluateAll(nodes => nodes.map(node => ({className:node.className,style:node.getAttribute('style'),rect:node.getBoundingClientRect().toJSON()})));
  report.selectState = await page.locator('.client-picker input').evaluateAll(nodes => nodes.map(node=>({expanded:node.getAttribute('aria-expanded'),rect:node.getBoundingClientRect().toJSON()})));
  await page.screenshot({path:path.join(root,'artifacts/screenshots/client-options-diagnostic.png')});
  report.error = error.stack; console.error(error.message); process.exitCode = 1;
} finally {
  const reportFile = report.motion === 'no-preference' ? 'client-options-normal-motion-browser.json' : 'client-options-browser.json';
  await fs.writeFile(path.join(root, 'artifacts/reports', reportFile), JSON.stringify(report, null, 2) + '\n');
  await browser.close();
}
