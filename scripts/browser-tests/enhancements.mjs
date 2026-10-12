import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const root = path.resolve(import.meta.dirname, '../..');
const state = JSON.parse(await fs.readFile(path.join(root, 'runtime/lab-state.json'), 'utf8'));
const fixture = JSON.parse(await fs.readFile(process.env.ENHANCEMENT_FIXTURE, 'utf8'));
assert.equal(fixture.url, 'http://127.0.0.1:18082', 'destructive checks confined to isolated fixture');
const base = fixture.url + state.base_path;
const api = base + 'traffic-monitor/api/v1/';
const report = { passed:false, fixture:fixture.fixture, checks:[], pageErrors:[], consoleErrors:[] };
const browser = await chromium.launch({headless:true});
const context = await browser.newContext({viewport:{width:1500,height:1150},locale:'zh-CN',timezoneId:'Asia/Shanghai',reducedMotion:'reduce'});
await context.addInitScript(() => { if (localStorage.getItem('dark-mode') === null) localStorage.setItem('dark-mode','false'); });
const page = await context.newPage();
const dataRequests=[];
page.on('request',r=>{if(r.url().startsWith(api))dataRequests.push(new URL(r.url()).pathname.split('/').pop());});
page.on('pageerror',e=>report.pageErrors.push(e.message));
page.on('console',m=>{if(m.type()==='error')report.consoleErrors.push(m.text());});
async function check(label, fn) {await fn();report.checks.push(label);console.log('PASS',label);}
async function obj(route,params={}) {const r=await context.request.get(api+route,{params});assert.equal(r.status(),200);return (await r.json()).obj;}
async function setRange(container, minute) {
  const inputs=container.locator('.ant-picker-input input');
  await inputs.nth(0).fill(minute);await inputs.nth(0).press('Enter');
  await inputs.nth(1).fill(minute);await inputs.nth(1).press('Enter');
}
try {
  const csrf=(await(await context.request.get(base+'csrf-token')).json()).obj;
  assert.equal((await(await context.request.post(base+'login',{form:{username:state.username,password:state.password},headers:{'X-CSRF-Token':csrf}})).json()).success,true);
  const rules=await obj('rules');assert.ok(rules.service_count>1400 && rules.rule_count>24000);
  await page.goto(base+'traffic-monitor/');
  await page.getByRole('heading',{name:'流量监测',exact:true}).waitFor();
  await page.getByText(/本地域名规则集/).waitFor();
  await page.getByRole('switch',{name:'自动刷新实时状态和历史统计'}).click();
  await check('initial summary view does not also scan hidden visit table',async()=>{
    assert.ok(dataRequests.includes('targets'));assert.ok(!dataRequests.includes('visits'));
    assert.equal(rules.ip_rule_count,14);
  });
  await check('deleted identities require history toggle and empty same-name identity is omitted',async()=>{
    const clients=await obj('clients');
    assert.ok(clients.some(c=>c.id==='fixture-alice'&&c.deleted));
    assert.ok(!clients.some(c=>c.id==='fixture-alice-removed'));
    const select=page.getByRole('combobox',{name:'选择客户端'});
    await select.click();
    assert.equal(await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({hasText:'已删除'}).count(),0);
    await select.press('Escape');
    await page.getByRole('checkbox',{name:'显示历史客户端',exact:true}).check();
    await select.click();
    assert.equal(await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({hasText:/^fixture-alice（已删除/}).count(),1);
    await page.screenshot({path:path.join(root,'artifacts/screenshots/monitor-history-clients.png')});
    await select.press('Escape');
    await page.getByRole('checkbox',{name:'显示历史客户端',exact:true}).uncheck();
  });
  await check('domain rules label Baidu GitHub YouTube Douyin and preserve unknown IP', async()=>{
    for(const name of ['百度','GitHub','YouTube','抖音']) await page.getByText(name,{exact:true}).first().waitFor();
    await page.getByText('203.0.113.7',{exact:true}).first().waitFor();
    const q=await obj('visits',{client_id:'fixture-alice',target:'百度'});
    assert.equal(q.total,2);assert.ok(q.items.every(v=>v.destination.name==='百度' && v.target!=='百度'));
    const domain = page.locator('.destination-raw').first();
    const style = await domain.evaluate(node => ({size:parseFloat(getComputedStyle(node).fontSize),wrap:getComputedStyle(node).overflowWrap}));
    assert.ok(style.size >= 14);assert.equal(style.wrap,'anywhere');
    assert.equal(await page.getByText('面板累计',{exact:true}).count(),0);
  });
  await check('target summary merges ports and service domains while isolating clients',async()=>{
    const q=await obj('targets',{client_id:'fixture-alice'});
    assert.equal(q.total,fixture.expected_groups_per_client);
    const local=q.items.find(x=>x.destination.name==='localhost');assert.equal(local.visit_count,3);assert.deepEqual(local.ports,[19090,19091]);
    const youtube=q.items.find(x=>x.destination.name==='YouTube');assert.equal(youtube.visit_count,3);assert.equal(youtube.target_count,3);
    for(const key of ['telegram','twitter','openai','taobao']) {
      const service=q.items.find(x=>x.destination.key==='service:'+key);
      assert.equal(service.visit_count,3);assert.equal(service.target_count,3);
    }
    assert.ok(q.items.every(x=>x.client_id==='fixture-alice'));
    const before=await obj('summary',{client_id:'fixture-alice'});assert.equal(before.total,fixture.expected_bytes_per_client);
    await page.screenshot({path:path.join(root,'artifacts/screenshots/monitor-service-summary.png'),fullPage:true});
  });
  await check('service name search filters both target summary and details',async()=>{
    await page.getByPlaceholder('搜索服务名、域名或 IP').fill('YouTube');
    await page.getByRole('button',{name:/查\s*询/}).click();
    await page.waitForFunction(()=>document.querySelectorAll('.targets-table tr[data-row-key]').length===2);
    await page.getByRole('tab',{name:'访问明细',exact:true}).click();
    await page.getByRole('tabpanel', {name:'访问明细',exact:true}).getByText('r1.googlevideo.com',{exact:true}).first().waitFor();
    assert.equal(await page.getByRole('tabpanel', {name:'访问明细',exact:true}).locator('tr[data-row-key]').count(),6);
  });
  await check('same-minute selection uses exact UTC bounds and adjacent minute stays empty',async()=>{
    await page.getByPlaceholder('搜索服务名、域名或 IP').fill('');
    await setRange(page.locator('.filter-card'),fixture.minute);
    const response=page.waitForResponse(r=>r.url().startsWith(api+'visits?') && r.status()===200);
    await page.getByRole('button',{name:/查\s*询/}).click();
    const r=await response;const url=new URL(r.url());
    assert.equal(Date.parse(url.searchParams.get('start')),Date.parse(fixture.minute_start));
    assert.equal(Date.parse(url.searchParams.get('end')),Date.parse(fixture.minute_end));
    assert.equal((await r.json()).obj.total,fixture.expected_visits_per_client*2);
    await setRange(page.locator('.filter-card'),fixture.empty_minute);
    const empty=page.waitForResponse(r=>r.url().startsWith(api+'summary?') && r.status()===200);
    await page.getByRole('button',{name:/查\s*询/}).click();
    const value=(await(await empty).json()).obj;assert.equal(value.total,0);assert.equal(value.visit_count,0);
    await page.getByRole('tabpanel',{name:'访问明细',exact:true}).locator('.ant-table-placeholder').waitFor();
    await setRange(page.locator('.filter-card'),fixture.minute);
    await page.getByRole('button',{name:/查\s*询/}).click();
    await page.getByRole('tabpanel',{name:'访问明细',exact:true}).locator('tr[data-row-key]').first().waitFor();
    await page.screenshot({path:path.join(root,'artifacts/screenshots/monitor-minute-filter.png'),fullPage:true});
  });
  await check('delete modal previews all destinations and requires explicit confirmation',async()=>{
    await page.getByRole('checkbox',{name:'显示历史客户端',exact:true}).check();
    await page.getByRole('combobox',{name:'选择客户端'}).click();
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({hasText:/^fixture-alice（已删除/}).click();
    const selected=page.waitForResponse(r=>r.url().startsWith(api+'summary?')&&new URL(r.url()).searchParams.get('client_id')==='fixture-alice'&&r.status()===200);
    await page.getByRole('button',{name:/查\s*询/}).click();
    await selected;
    await page.getByRole('button',{name:'删除日志与流量'}).click();
    const modal=page.getByRole('dialog');
    await modal.getByRole('checkbox',{name:'显示历史客户端',exact:true}).check();
    await modal.getByRole('combobox',{name:'删除哪些客户端'}).click();
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({hasText:/^fixture-alice（已删除/}).click();
    await setRange(modal,fixture.minute);
    assert.equal(await modal.getByRole('button',{name:'确认删除'}).isEnabled(),false);
    const previewResponse=page.waitForResponse(r=>r.url().startsWith(api+'history/preview?') && r.status()===200);
    await modal.getByRole('button',{name:'预览删除范围'}).click();
    const preview=(await(await previewResponse).json()).obj;
    assert.equal(Date.parse(preview.start),Date.parse(fixture.minute_start));
    assert.equal(Date.parse(preview.end),Date.parse(fixture.minute_end));
    await modal.getByText('3.00 MiB',{exact:true}).waitFor();
    await modal.getByText(fixture.expected_visits_per_client+' 条',{exact:true}).waitFor();
    assert.equal(await modal.getByRole('button',{name:'确认删除'}).isEnabled(),false);
    await page.screenshot({path:path.join(root,'artifacts/screenshots/monitor-delete-preview.png')});
    await modal.getByRole('checkbox',{name:'确认删除所选范围',exact:true}).check();
    assert.equal(await modal.getByRole('button',{name:'确认删除'}).isEnabled(),true);
    await modal.getByRole('button',{name:'确认删除'}).click();
    await modal.waitFor({state:'detached'});
    await page.getByText(/所选范围的访问记录和监测流量已删除/).waitFor();
  });
  await check('confirmed deletion updates bytes counts details and targets without touching another client',async()=>{
    const removed=await obj('summary',{client_id:'fixture-alice'});assert.equal(removed.total,0);assert.equal(removed.visit_count,0);assert.ok(removed.quality.includes('history_deleted'));
    assert.equal((await obj('visits',{client_id:'fixture-alice'})).total,0);
    assert.equal((await obj('targets',{client_id:'fixture-alice'})).total,0);
    const kept=await obj('summary',{client_id:'fixture-bob'});assert.equal(kept.total,fixture.expected_bytes_per_client);assert.equal(kept.visit_count,fixture.expected_visits_per_client);
    const clients=await obj('clients');
    assert.ok(!clients.some(c=>c.id==='fixture-alice'));
    assert.ok(clients.some(c=>c.id==='fixture-bob'));
    await page.locator('.filter-card .client-picker').getByText('全部客户端',{exact:true}).waitFor();
  });
  await check('all-client minute deletion clears remaining records and the selected-period total',async()=>{
    await page.getByRole('button',{name:'删除日志与流量'}).click();
    const modal=page.getByRole('dialog');
    await modal.getByText('所有客户端（包含历史及未匹配记录）',{exact:true}).waitFor();
    await setRange(modal,fixture.minute);
    const response=page.waitForResponse(r=>r.url().startsWith(api+'history/preview?')&&r.status()===200);
    await modal.getByRole('button',{name:'预览删除范围'}).click();
    const r=await response;assert.equal(new URL(r.url()).searchParams.has('client_id'),false);
    const preview=(await r.json()).obj;
    assert.equal(preview.total,fixture.expected_bytes_per_client);
    assert.equal(preview.visit_count,fixture.expected_visits_per_client);
    await modal.getByRole('checkbox',{name:'确认删除所选范围',exact:true}).check();
    await modal.getByRole('button',{name:'确认删除'}).click();
    await modal.waitFor({state:'detached'});
    const range={start:fixture.minute_start,end:fixture.minute_end};
    assert.equal((await obj('summary',range)).total,0);
    assert.equal((await obj('visits',range)).total,0);
    assert.ok(!(await obj('clients')).some(c=>['fixture-alice','fixture-bob','fixture-alice-removed'].includes(c.id)));
    await page.locator('.metric').filter({has:page.getByText('合计流量',{exact:true})}).getByText('0 B',{exact:true}).waitFor();
  });
  await check('new controls fit mobile and dark theme',async()=>{
    await page.setViewportSize({width:390,height:844});await page.reload();
    await page.getByText(/本地域名规则集/).waitFor();
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+2));
    await page.getByRole('button',{name:'删除日志与流量'}).click();
    await page.getByRole('dialog').waitFor();
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+2));
    await page.screenshot({path:path.join(root,'artifacts/screenshots/monitor-delete-mobile.png')});
    await page.getByRole('dialog').getByRole('button',{name:/取\s*消/}).click();
    await page.evaluate(()=>localStorage.setItem('dark-mode','true'));await page.reload();
    await page.getByRole('heading',{name:'流量监测',exact:true}).waitFor();
    assert.ok((await page.locator('html').getAttribute('data-theme')).includes('dark'));
  });
  assert.deepEqual(report.pageErrors,[]);assert.deepEqual(report.consoleErrors,[]);
  report.passed=true;
} catch(error) {
  report.error=error.stack;console.error(error.message);process.exitCode=1;
  await page.screenshot({path:path.join(root,'artifacts/screenshots/enhancements-failure.png'),fullPage:true}).catch(()=>{});
} finally {
  await fs.writeFile(path.join(root,'artifacts/reports/enhancement-browser-checks.json'),JSON.stringify(report,null,2)+'\n');
  await browser.close();
}
