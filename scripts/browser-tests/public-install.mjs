import {chromium} from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const state = JSON.parse(await fs.readFile(process.env.PANEL_STATE_FILE, 'utf8'));
const fixture = process.env.PUBLIC_INSTALL_FIXTURE;
const base = state.public_url + state.base_path;
const report = {passed: false, checks: [], errors: []};
const browser = await chromium.launch({headless: true});
const context = await browser.newContext({viewport: {width: 1440, height: 1000}, locale: 'zh-CN', timezoneId: 'Asia/Shanghai'});
await context.addInitScript(() => localStorage.setItem('lang', 'zh-CN'));
const page = await context.newPage();
page.on('pageerror', error => report.errors.push(error.message));
try {
  const csrf = (await (await context.request.get(base + 'csrf-token')).json()).obj;
  const login = await context.request.post(base + 'login', {
    form: {username: state.username, password: state.password}, headers: {'X-CSRF-Token': csrf},
  });
  assert.equal((await login.json()).success, true);
  await page.goto(base + 'panel/?monitor=traffic');
  const frame = page.frameLocator('#traffic-monitor-frame');
  await frame.getByRole('heading', {name: '流量监测', exact: true}).waitFor({timeout: 45000});
  await frame.getByText('采集正常', {exact: true}).waitFor();
  assert.equal(await page.locator('.sider-nav .tm-menu-item').count(), 1);
  report.checks.push('fresh login and embedded monitor retain the official sidebar');

  const inputs = frame.locator('.ant-picker-input input');
  assert.match(await inputs.nth(0).inputValue(), /\d{2}:\d{2}/);
  await frame.getByRole('tab', {name: '访问明细', exact: true}).click();
  await frame.getByText('实时在线客户端', {exact: true}).waitFor();
  await frame.getByRole('button', {name: /删除日志与流量/}).click();
  await frame.getByRole('dialog').waitFor();
  await frame.getByRole('dialog').getByRole('button', {name: /取\s*消/}).click();
  report.checks.push('minute selection, access detail and deletion dialog load');

  await page.reload();
  await frame.getByRole('heading', {name: '流量监测', exact: true}).waitFor();
  await page.screenshot({path: path.join(fixture, 'monitor-desktop.png'), fullPage: true});
  await page.setViewportSize({width: 390, height: 844});
  await frame.getByRole('heading', {name: '流量监测', exact: true}).waitFor();
  await page.screenshot({path: path.join(fixture, 'monitor-mobile.png'), fullPage: true});
  report.checks.push('refresh preserves the tab and the mobile page renders');

  await page.goto(base + 'traffic-monitor/');
  await page.getByRole('heading', {name: '流量监测', exact: true}).waitFor();
  await context.clearCookies();
  assert.equal((await context.request.get(base + 'traffic-monitor/api/v1/clients')).status(), 401);
  report.checks.push('independent page loads and clearing the session denies data access');
  assert.deepEqual(report.errors, []);
  report.passed = true;
} finally {
  await fs.writeFile(path.join(fixture, 'browser-report.json'), JSON.stringify(report, null, 2) + '\n');
  await browser.close();
}
