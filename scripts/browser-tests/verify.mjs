import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const root = path.resolve(import.meta.dirname, '../..');
const state = JSON.parse(await fs.readFile(path.join(root, 'runtime/lab-state.json'), 'utf8'));
const origin = process.env.MONITOR_URL || state.public_url.replace('localhost', '127.0.0.1');
const base = new URL(state.base_path, origin).href;
const report = { passed: false, checks: [], pageErrors: [], consoleErrors: [], requestFailures: [] };
let phase = 'startup';
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1600, height: 1100 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai' });
await context.addInitScript(() => { localStorage.setItem('lang', 'zh-CN'); });
const page = await context.newPage();
page.on('pageerror', error => report.pageErrors.push(error.message));
page.on('console', message => { if (message.type() === 'error') report.consoleErrors.push(message.text()); });
page.on('requestfailed', request => report.requestFailures.push({ phase, path: new URL(request.url()).pathname, error: request.failure()?.errorText }));
async function check(label, fn) { phase = label; await fn(); report.checks.push(label); console.log('PASS', label); }
const dateText = value => new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value));
async function selectDate(frame, date) {
  const inputs=frame.locator('.ant-picker-input input');
  await inputs.nth(0).fill(date+' 00:00');await inputs.nth(0).press('Enter');
  await inputs.nth(1).fill(date+' 23:59');await inputs.nth(1).press('Enter');
}
try {
  const csrfResponse = await context.request.get(base + 'csrf-token');
  const csrf = (await csrfResponse.json()).obj;
  const login = await context.request.post(base + 'login', { form: { username: state.username, password: state.password }, headers: { 'X-CSRF-Token': csrf, 'X-Requested-With': 'XMLHttpRequest' } });
  assert.equal((await login.json()).success, true, 'official login');
  const health=(await(await context.request.get(base+'traffic-monitor/api/v1/health')).json()).obj;
  const clients=(await(await context.request.get(base+'traffic-monitor/api/v1/clients')).json()).obj;
  const bob=clients.find(c=>c.email==='monitor-test-bob');assert.ok(bob);
  const last=(await(await context.request.get(base+'traffic-monitor/api/v1/summary',{params:{client_id:bob.id}})).json()).obj.last_visit_at;
  assert.ok(last,'retained original test visits');
  await check('official dashboard embeds monitor and preserves sidebar/header', async () => {
    await page.goto(base + 'panel/?monitor=traffic');
    await page.locator('#traffic-monitor-frame').waitFor({ state: 'visible', timeout: 45000 });
    const frame = page.frameLocator('#traffic-monitor-frame');
    await frame.getByRole('heading', { name: '流量监测', exact: true }).waitFor();
    await frame.getByText('采集正常', { exact: true }).waitFor({ timeout: 30000 });
    assert.equal(await page.locator('.sider-nav .tm-menu-item').count(), 1);
    assert.equal(await page.locator('.ov-bar').isVisible(), true);
    assert.equal(await page.locator('.ov-grid').isVisible().catch(() => false), false);
    await page.mouse.move(1000, 80);
    await page.waitForTimeout(500);
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-desktop.png'), fullPage: true });
  });
  await check('client and target filters query real recorded visits', async () => {
    const frame = page.frameLocator('#traffic-monitor-frame');
    await frame.getByRole('tab', { name: '访问明细', exact: true }).click();
    await selectDate(frame,dateText(last));
    await frame.getByPlaceholder('搜索服务名、域名或 IP').fill('localhost');
    await frame.getByRole('button', { name: /查\s*询/ }).click();
    await frame.getByRole('cell', { name: 'localhost', exact: true }).first().waitFor();
    const rows = await frame.locator('.ant-table-tbody tr[data-row-key]').count();
    assert.ok(rows >= 2, 'two clients have domain records');
    await frame.getByPlaceholder('搜索服务名、域名或 IP').fill('no-such-fixture.invalid');
    await frame.getByRole('button', { name: /查\s*询/ }).click();
    await frame.getByRole('tabpanel', { name: '访问明细', exact: true }).locator('.ant-table-placeholder').waitFor();
    await frame.getByPlaceholder('搜索服务名、域名或 IP').fill('');
    await frame.getByRole('button', { name: /查\s*询/ }).click();
  });
  await check('dark theme propagates into monitor', async () => {
    await page.evaluate(() => document.body.classList.add('dark'));
    await page.frameLocator('#traffic-monitor-frame').locator('.monitor-app').waitFor();
    await page.waitForTimeout(700);
    const dark = await page.frameLocator('#traffic-monitor-frame').locator('html').getAttribute('data-theme');
    assert.ok(dark.includes('dark'), dark);
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-dark.png'), fullPage: true });
    await page.evaluate(() => document.body.classList.remove('dark'));
  });
  await check('light theme and pinned sidebar preserve content width', async () => {
    await page.evaluate(() => { document.body.classList.remove('dark'); document.documentElement.dataset.theme = 'light'; });
    await page.mouse.move(30, 100);
    await page.locator('.sidebar-pin').click();
    await page.mouse.move(1100, 100);
    await page.waitForTimeout(700);
    const frameBox = await page.locator('#traffic-monitor-frame').boundingBox();
    assert.ok(frameBox.x >= 215, `pinned sidebar content origin: ${frameBox.x}`);
    const mode = await page.frameLocator('#traffic-monitor-frame').locator('html').getAttribute('data-theme');
    assert.equal(mode, 'light');
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-light.png'), fullPage: true });
    await page.locator('.sidebar-pin').click();
    await page.mouse.move(1100, 100);
  });
  await check('specified client and pre-monitoring date produce explicit states', async () => {
    const frame = page.frameLocator('#traffic-monitor-frame');
    await frame.getByRole('combobox', { name: '选择客户端' }).click();
    await frame.locator('.ant-select-item-option').filter({ hasText: /^monitor-test-bob$/ }).click();
    await frame.getByRole('button', { name: /查\s*询/ }).click();
    await frame.getByRole('cell', { name: 'monitor-test-bob', exact: true }).first().waitFor();
    assert.equal(await frame.getByRole('cell', { name: 'monitor-test-alice', exact: true }).count(), 0);
    const beforeMonitoring = dateText(Date.parse(health.monitor_started_at)-86400000);
    await selectDate(frame,beforeMonitoring);
    await frame.getByRole('button', { name: /查\s*询/ }).click();
    await frame.getByRole('tabpanel', { name: '访问明细', exact: true }).locator('.ant-table-placeholder').waitFor();
    await frame.getByText(/接入前/).first().waitFor();
    const metrics = await frame.locator('.metric-value').allTextContents();
    assert.ok(metrics.length >= 3 && metrics.slice(0, 3).every(value => value.includes('—')), `pre-monitor metrics: ${metrics}`);
    await page.reload();
    await page.locator('#traffic-monitor-frame').waitFor();
  });
  await check('history navigation and refresh retain correct selected tab', async () => {
    await page.goto(base + 'panel/');
    await page.locator('.sider-nav .tm-menu-item').waitFor();
    await page.locator('.sider-nav .tm-menu-item').click();
    await page.locator('#traffic-monitor-frame').waitFor();
    await page.goBack();
    await page.waitForFunction(() => !document.getElementById('traffic-monitor-frame'));
    await page.goForward();
    await page.locator('#traffic-monitor-frame').waitFor();
    await page.reload();
    await page.locator('#traffic-monitor-frame').waitFor();
    assert.equal(await page.locator('.sider-nav .tm-menu-item').count(), 1);
  });
  await check('mobile layout fits viewport', async () => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await page.locator('#traffic-monitor-frame').waitFor();
    await page.frameLocator('#traffic-monitor-frame').getByRole('heading', { name: '流量监测', exact: true }).waitFor();
    await page.frameLocator('#traffic-monitor-frame').getByText('采集正常', { exact: true }).waitFor();
    await page.frameLocator('#traffic-monitor-frame').locator('.ant-spin-blur').waitFor({ state: 'detached' });
    const widths = await page.evaluate(() => [document.documentElement.scrollWidth, innerWidth]);
    assert.ok(widths[0] <= widths[1] + 2, `horizontal overflow: ${widths}`);
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-mobile.png'), fullPage: true });
    await page.locator('.drawer-handle').click();
    await page.locator('.drawer-nav .tm-menu-item').waitFor({ state: 'visible' });
    await page.locator('.drawer-nav .tm-menu-item').click();
    await page.locator('.drawer-handle').waitFor({ state: 'visible' });
    assert.equal(await page.locator('#traffic-monitor-frame').count(), 1);
  });
  await check('unknown panel version preserves dashboard and fallback', async () => {
    await page.setViewportSize({ width: 1600, height: 1100 });
    await page.evaluate(() => { window.X_UI_CUR_VER = '99.0.0'; document.body.classList.toggle('dark'); });
    await page.locator('#traffic-monitor-fallback').waitFor({ timeout: 15000 });
    assert.equal(await page.locator('#traffic-monitor-frame').count(), 0);
    assert.equal(await page.locator('.ov-bar').isVisible(), true);
    await page.reload();
  });
  await check('independent monitor page works under same login', async () => {
    await page.goto(base + 'traffic-monitor/');
    await page.getByRole('heading', { name: '流量监测', exact: true }).waitFor();
    await page.screenshot({ path: path.join(root, 'artifacts/screenshots/monitor-independent.png'), fullPage: true });
  });
  await check('logout revokes monitoring API session', async () => {
    const csrf2 = (await (await context.request.get(base + 'csrf-token')).json()).obj;
    const logout = await context.request.post(base + 'logout', { headers: { 'X-CSRF-Token': csrf2 } });
    assert.ok(logout.ok());
    const denied = await context.request.get(base + 'traffic-monitor/api/v1/clients');
    assert.equal(denied.status(), 401);
  });
  assert.deepEqual(report.pageErrors, [], 'browser has no unhandled errors');
  report.passed = true;
} catch (error) {
  report.error = error.stack;
  await page.screenshot({ path: path.join(root, 'artifacts/screenshots/browser-failure.png'), fullPage: true }).catch(() => {});
  console.error(error.message);
  process.exitCode = 1;
} finally {
  await fs.writeFile(path.join(root, 'artifacts/reports/browser-checks.json'), JSON.stringify(report, null, 2) + '\n');
  await browser.close();
}
