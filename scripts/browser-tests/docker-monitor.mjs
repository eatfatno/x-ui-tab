import {chromium} from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../..');
const state=JSON.parse(await fs.readFile(process.env.PANEL_STATE_FILE,'utf8'));
const base=state.public_url+state.base_path;
const report={passed:false,checks:[],errors:[]};
const browser=await chromium.launch({headless:true});
const context=await browser.newContext({viewport:{width:1500,height:1050},locale:'zh-CN',timezoneId:'Asia/Shanghai'});
await context.addInitScript(()=>localStorage.setItem('lang','zh-CN'));
const page=await context.newPage();
page.on('pageerror',e=>report.errors.push(e.message));
async function check(label,fn){await fn();report.checks.push(label);console.log('PASS',label);}
try{
  const csrf=(await(await context.request.get(base+'csrf-token')).json()).obj;
  assert.equal((await(await context.request.post(base+'login',{form:{username:state.username,password:state.password},headers:{'X-CSRF-Token':csrf}})).json()).success,true);
  await page.goto(base+'panel/?monitor=traffic');
  const frame=page.frameLocator('#traffic-monitor-frame');
  await check('container: embedded monitor and grouped target records',async()=>{
    await frame.getByRole('heading',{name:'流量监测',exact:true}).waitFor({timeout:45000});
    await frame.getByText('采集正常',{exact:true}).waitFor();
    await frame.getByRole('cell',{name:'localhost',exact:true}).first().waitFor();
    assert.equal(await page.locator('.sider-nav .tm-menu-item').count(),1);
  });
  await check('container: minute picker, visits, realtime and delete preview',async()=>{
    const inputs=frame.locator('.ant-picker-input input');
    assert.match(await inputs.nth(0).inputValue(),/\d{2}:\d{2}/);
    await frame.getByRole('tab',{name:'访问明细',exact:true}).click();
    await frame.getByRole('cell',{name:'localhost',exact:true}).first().waitFor();
    await frame.getByText('实时在线客户端',{exact:true}).waitFor();
    await frame.getByRole('button',{name:/删除日志与流量/}).click();
    await frame.getByRole('dialog').waitFor();
    await frame.getByRole('dialog').getByRole('button',{name:/取\s*消/}).click();
  });
  await check('container: dark mode, mobile and refresh',async()=>{
    await page.evaluate(()=>document.body.classList.add('dark'));
    await page.waitForTimeout(400);
    await page.setViewportSize({width:390,height:844});
    await frame.getByRole('heading',{name:'流量监测',exact:true}).waitFor();
    await page.screenshot({path:path.join(root,'artifacts/screenshots/docker-monitor-mobile.png'),fullPage:true});
    await page.setViewportSize({width:1500,height:1050});
    await page.reload();await frame.getByRole('heading',{name:'流量监测',exact:true}).waitFor();
    await page.screenshot({path:path.join(root,'artifacts/screenshots/docker-monitor-desktop.png'),fullPage:true});
  });
  await check('container: independent page and login gate after session removal',async()=>{
    await page.goto(base+'traffic-monitor/');await page.getByRole('heading',{name:'流量监测',exact:true}).waitFor();
    await context.clearCookies();
    assert.equal((await context.request.get(base+'traffic-monitor/api/v1/clients')).status(),401);
  });
  assert.deepEqual(report.errors,[]);report.passed=true;
}finally{await fs.writeFile(path.join(root,'artifacts/reports/docker-monitor-browser.json'),JSON.stringify(report,null,2)+'\n');await browser.close();}
