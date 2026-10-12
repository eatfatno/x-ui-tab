// Exercises official lazy routes and dialogs through the monitoring proxy.
// Only opens/cancels configuration dialogs; existing clients and counters stay intact.
import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const root = path.resolve(import.meta.dirname, '../..');
const state = JSON.parse(await fs.readFile(process.env.PANEL_STATE_FILE || path.join(root, 'runtime/lab-state.json'), 'utf8'));
const base = new URL(state.base_path, process.env.PANEL_URL || state.public_url).href;
const report = {passed:false,base,checks:[],errors:[],failures:[],httpErrors:[],assets:[],websocket:false};
const browser = await chromium.launch({headless:true});
const context = await browser.newContext({viewport:{width:1600,height:1050},locale:'zh-CN',timezoneId:'Asia/Shanghai'});
await context.addInitScript(()=>localStorage.setItem('lang','zh-CN'));
const page = await context.newPage();
page.setDefaultTimeout(45000);
let phase = 'login';
page.on('pageerror',e=>report.errors.push({phase,message:e.message}));
page.on('requestfailed',r=>report.failures.push({phase,path:new URL(r.url()).pathname,error:r.failure()?.errorText}));
page.on('response',r=>{const p=new URL(r.url()).pathname;if(p.includes('/assets/'))report.assets.push({path:p,status:r.status()});if(r.status()>=400)report.httpErrors.push({phase,path:p,status:r.status()});});
page.on('websocket',ws=>{ws.on('framereceived',()=>{report.websocket=true;});});
async function check(label,fn){phase=label;await fn();assert.equal(await page.getByText('Unexpected Application Error!',{exact:true}).count(),0);report.checks.push(label);console.log('PASS',label);}
async function ready(){await page.locator('.sider-nav').waitFor();await page.waitForFunction(()=>!document.querySelector('.ant-spin-spinning'));}
async function close(){const modal=page.getByRole('dialog').last();const x=modal.locator('.ant-modal-close');if(await x.count())await x.click();else await modal.getByRole('button',{name:/取\s*消|关\s*闭|Close|Cancel/}).first().click();await modal.waitFor({state:'hidden'});}
async function nav(name,route){
  await page.locator('.sider-nav').getByText(name,{exact:true}).click();
  if(route==='settings')await page.locator('.sider-nav').getByText('常规',{exact:true}).click();
  if(route==='xray')await page.locator('.sider-nav').getByText('基础配置',{exact:true}).click();
  await page.waitForURL(url=>url.pathname.replace(/\/$/,'')===new URL(base+'panel/'+route).pathname.replace(/\/$/,''));
  const pageClass={inbounds:'.inbounds-page',clients:'.clients-page',groups:'.groups-page',nodes:'.nodes-page',hosts:'.hosts-page',settings:'.settings-page',xray:'.xray-page',outbound:'.xray-page',routing:'.xray-page','api-docs':'.api-docs-page',sponsors:'.sponsors-page','':'.index-page'}[route];
  await page.locator(pageClass).waitFor();await ready();
}
try{
  const csrf=(await(await context.request.get(base+'csrf-token')).json()).obj;
  assert.equal((await(await context.request.post(base+'login',{form:{username:state.username,password:state.password},headers:{'X-CSRF-Token':csrf}})).json()).success,true);
  await check('clients route and QR dialog load all dynamic modules',async()=>{
    await page.goto(base+'panel/clients');await ready();
    await page.mouse.move(25,100);await page.locator('.sidebar-pin').click();await page.mouse.move(1000,80);
    const row=page.locator('tr[data-row-key]').filter({hasText:process.env.QR_CLIENT || 'monitor-test-alice'}).first();await row.waitFor();
    await row.getByRole('button',{name:/二维码|QR/}).click();
    await page.getByRole('dialog').waitFor();
    if(process.env.QR_CLIENT)await page.getByRole('dialog').locator('.ant-qrcode').first().waitFor();
    else await page.getByRole('dialog').getByText(/没有订阅|订阅.*ID|未.*订阅/).waitFor();
    await close();
  });
  await check('client info and edit dialogs open and cancel',async()=>{
    const row=page.locator('tr[data-row-key]').filter({hasText:'monitor-test-alice'}).first();
    await row.getByRole('button',{name:/客户端信息/}).click();await page.getByRole('dialog').waitFor();await close();
    await row.getByRole('button',{name:'编辑',exact:true}).click();await page.getByRole('dialog').waitFor();await close();
    await page.getByRole('button',{name:'添加客户端',exact:true}).click();await page.getByRole('dialog').waitFor();await close();
  });
  await check('inbounds list, edit and new inbound dialogs',async()=>{
    await nav('入站','inbounds');
    const row=page.locator('tr[data-row-key]').filter({hasText:'本地验收'}).first();await row.waitFor();await row.getByRole('button',{name:'编辑',exact:true}).click();await page.getByRole('dialog').waitFor();await close();
    await page.getByRole('button',{name:/^添加入站$/}).click();await page.getByRole('dialog').waitFor();await close();
  });
  for(const [name,route] of [['系统状态',''],['分组','groups'],['节点','nodes'],['主机','hosts'],['出站','outbound'],['路由','routing'],['面板设置','settings'],['Xray 配置','xray'],['API 文档','api-docs'],['赞助商','sponsors']]){
    await check('official sidebar: '+name,async()=>{await nav(name,route);await page.waitForTimeout(400);});
    if(route==='settings')await check('settings subsections open without saving',async()=>{
      for(const name of ['安全设定','Telegram 机器人','邮件','Discord 机器人','订阅设置','常规']){
        await page.locator('.sider-nav').getByText(name,{exact:true}).click();await ready();
        assert.equal(await page.getByText('Unexpected Application Error!',{exact:true}).count(),0);
      }
    });
    if(route==='xray')await check('Xray basic, balancer, DNS and advanced sections',async()=>{
      for(const name of ['负载均衡','DNS','高级配置','基础配置']){
        await page.locator('.sider-nav').getByText(name,{exact:true}).click();await ready();
        assert.equal(await page.getByText('Unexpected Application Error!',{exact:true}).count(),0);
      }
    });
  }
  await check('monitor -> clients -> QR -> back -> refresh',async()=>{
    await page.locator('.sider-nav .tm-menu-item').click();await page.locator('#traffic-monitor-frame').waitFor();
    await nav('客户端','clients');await page.waitForFunction(()=>!document.getElementById('traffic-monitor-frame'));
    const row=page.locator('tr[data-row-key]').filter({hasText:'monitor-test-alice'}).first();
    await row.getByRole('button',{name:/二维码|QR/}).click();await page.getByRole('dialog').waitFor();await close();
    await page.goBack();await page.locator('#traffic-monitor-frame').waitFor();await page.goForward();await ready();await page.reload();await ready();
  });
  await check('official WebSocket bridge receives frames',async()=>assert.equal(report.websocket,true));
  assert.deepEqual(report.errors,[]);assert.deepEqual(report.httpErrors,[]);
  assert.equal(report.failures.filter(r=>r.error!=='net::ERR_ABORTED').length,0);
  report.uniqueAssetCount=new Set(report.assets.map(a=>a.path)).size;
  report.passed=true;
}catch(e){report.error=e.stack;report.lastURL=page.url();report.visibleMenu=await page.locator('.sider-nav').allTextContents();console.error(e.message);process.exitCode=1;}
finally{await fs.writeFile(process.env.BROWSER_REPORT_FILE || path.join(root,'artifacts/reports/official-panel-browser.json'),JSON.stringify(report,null,2)+'\n');await browser.close();}
