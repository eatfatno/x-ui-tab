import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const root = path.resolve(import.meta.dirname, '../..');
const state = JSON.parse(await fs.readFile(path.join(root, 'runtime/lab-state.json'), 'utf8'));
const fixture = JSON.parse(await fs.readFile(process.env.REALTIME_FIXTURE, 'utf8'));
const base = new URL(state.base_path, state.public_url.replace('localhost', '127.0.0.1')).href;
const api = base + 'traffic-monitor/api/v1/';
const report = { passed: false, checks: [], pageErrors: [], consoleErrors: [] };
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1500, height: 1100 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai', reducedMotion: 'reduce' });
await context.addInitScript(() => { try { if (localStorage.getItem('dark-mode') === null) localStorage.setItem('dark-mode', 'false'); } catch {} });
const page = await context.newPage();
page.on('pageerror', e => report.pageErrors.push(e.message));
page.on('console', m => { if (m.type() === 'error') report.consoleErrors.push(m.text()); });
const liveRequests = [];
page.on('request', r => { if (r.url().startsWith(api + 'realtime')) liveRequests.push(r.url()); });
async function check(label, fn) { await fn(); report.checks.push(label); console.log('PASS', label); }
const row = () => page.locator('.realtime-table tr[data-row-key]').filter({ hasText: fixture.email });
try {
  const csrf = (await (await context.request.get(base + 'csrf-token')).json()).obj;
  assert.equal((await (await context.request.post(base + 'login', { form: { username: state.username, password: state.password }, headers: { 'X-CSRF-Token': csrf } })).json()).success, true);
  await page.goto(base + 'traffic-monitor/');
  await row().getByText('在线', { exact: true }).waitFor();
  await check('real online transfer shows upload/download rates and latest interval bytes', async () => {
    const rates = await row().locator('strong').allTextContents();
    assert.equal(rates.length, 2);
    assert.ok(rates.every(r => /\/s$/.test(r) && !/^0 B/.test(r)), rates.join(','));
    const live = (await (await context.request.get(api + 'realtime', { params: { client_id: fixture.client_id } })).json()).obj;
    const item = live.items[0];
    assert.equal(item.status, 'online');
    assert.ok(item.up > 0 && item.down > 0);
    assert.ok(Math.abs(item.up / live.sample_seconds - item.up_per_second) < .001);
    assert.ok(Math.abs(item.down / live.sample_seconds - item.down_per_second) < .001);
    report.liveSample = live;
  });
  await check('single selected-period total replaces panel cumulative card and chart fills width', async () => {
    assert.equal(await page.getByText('面板累计', { exact: true }).count(), 0);
    assert.equal(await page.getByText('合计流量', { exact: true }).count(), 1);
    const chart = await page.locator('.trend-card').boundingBox();
    const metrics = await page.locator('.metrics').boundingBox();
    assert.ok(Math.abs(chart.width - metrics.width) <= 2);
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-realtime-light.png'), fullPage: true });
  });
  await check('live data polls independently and client filter excludes date and target parameters', async () => {
    const before = liveRequests.length;
    await page.waitForTimeout(11500);
    assert.ok(liveRequests.length > before, '10-second refresh ran');
    await page.getByRole('combobox', { name: '选择客户端' }).click();
    await page.locator('.ant-select-item-option').filter({ hasText: new RegExp('^' + fixture.email + '$') }).click();
    await page.getByPlaceholder('搜索服务名、域名或 IP').fill('not-a-real-service.invalid');
    await page.getByRole('button', { name: /查\s*询/ }).click();
    await row().getByText('在线', { exact: true }).waitFor();
    assert.equal(await page.locator('.realtime-table tr[data-row-key]').count(), 1);
    const url = new URL(liveRequests.at(-1));
    assert.equal(url.searchParams.get('client_id'), fixture.client_id);
    assert.deepEqual([...url.searchParams.keys()], ['client_id']);
    await page.getByRole('switch', { name: '自动刷新实时状态和历史统计' }).click();
    await row().waitFor();
    await page.getByRole('switch', { name: '仅显示在线客户端' }).click();
    assert.equal(await page.locator('.realtime-table tr[data-row-key]').count(), 1);
  });
  await check('mobile and dark realtime layout stays within viewport', async () => {
    await page.setViewportSize({ width: 390, height: 844 });
    await row().waitFor();
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 2));
    await page.locator('.realtime-card').scrollIntoViewIfNeeded();
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-realtime-mobile.png') });
    await page.evaluate(() => localStorage.setItem('dark-mode', 'true'));
    await page.reload();
    await row().getByText('在线', { exact: true }).waitFor();
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark');
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 2));
    await page.setViewportSize({ width: 1500, height: 1100 });
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-realtime-dark.png'), fullPage: true });
  });
  await check('realtime endpoint requires browser session after logout', async () => {
    await page.goto('about:blank');
    const csrf = (await (await context.request.get(base + 'csrf-token')).json()).obj;
    await context.request.post(base + 'logout', { headers: { 'X-CSRF-Token': csrf } });
    assert.equal((await context.request.get(api + 'realtime')).status(), 401);
  });
  assert.deepEqual(report.pageErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  report.passed = true;
} catch (error) {
  report.error = error.stack; console.error(error.message); process.exitCode = 1;
  await page.screenshot({ path: path.join(root, 'artifacts/screenshots/realtime-failure.png'), fullPage: true }).catch(() => {});
} finally {
  await fs.writeFile(path.join(root, 'artifacts/reports/realtime-browser.json'), JSON.stringify(report, null, 2) + '\n');
  await browser.close();
}
