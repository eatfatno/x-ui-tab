import { chromium } from 'playwright';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
const root=path.resolve(import.meta.dirname,'../..');
const state=JSON.parse(await fs.readFile(path.join(root,'runtime/lab-state.json'),'utf8'));
const base=new URL(state.base_path,state.public_url).href;
const baseline=process.env.RECOVERY_BASELINE==='1';
const report={passed:false,baseline,checks:[],expectedFailures:[]};
const browser=await chromium.launch({headless:true});
const context=await browser.newContext({viewport:{width:1600,height:1000},locale:'zh-CN'});
await context.addInitScript(()=>localStorage.setItem('lang','zh-CN'));
const page=await context.newPage();page.setDefaultTimeout(60000);
page.on('requestfailed',r=>report.expectedFailures.push({path:new URL(r.url()).pathname,error:r.failure()?.errorText}));
try{
  const csrf=(await(await context.request.get(base+'csrf-token')).json()).obj;
  assert.equal((await(await context.request.post(base+'login',{form:{username:state.username,password:state.password},headers:{'X-CSRF-Token':csrf}})).json()).success,true);
  await page.route('**/assets/ClientQrModal-*.js',route=>route.abort('failed'),{times:1});
  await page.goto(base+'panel/clients');await page.locator('.sidebar-pin').waitFor();
  await page.mouse.move(25,100);await page.locator('.sidebar-pin').click();await page.mouse.move(1000,80);
  await page.evaluate(()=>{
    const entry=[...document.scripts].find(s=>s.type==='module'&&s.src.includes('/assets/index-'));
    if(!entry)throw Error('Missing panel entry');
    sessionStorage.setItem(`xui:chunk-recovery:${window.X_UI_BASE_PATH||'/'}:${entry.src}`,'1');
  });
  const qr=()=>page.locator('tr[data-row-key]').filter({hasText:'monitor-test-alice'}).first().getByRole('button',{name:'二维码',exact:true});
  await qr().click();
  if(baseline){
    await page.getByText('Unexpected Application Error!',{exact:true}).waitFor();
    await page.getByRole('heading',{name:/Failed to fetch dynamically imported module/}).waitFor();
    report.checks.push('same missing QR module error reproduced after official recovery allowance is spent');
  }else{
    await page.getByRole('alert').getByText('面板页面加载中断',{exact:true}).waitFor();
    await page.waitForTimeout(1000);
    assert.equal(await page.locator('#traffic-monitor-panel-recovery').count(),1);
    assert.equal(await page.locator('#traffic-monitor-panel-recovery a').getAttribute('href'),base+'traffic-monitor/');
    await page.getByRole('button',{name:'重新加载页面',exact:true}).click();
    await page.locator('.sider-nav').waitFor();
    await qr().click();await page.getByRole('dialog').waitFor();
    assert.equal(await page.getByText('Unexpected Application Error!',{exact:true}).count(),0);
    assert.equal(await page.locator('#traffic-monitor-panel-recovery').count(),0);
    report.checks.push('interrupted QR module offers reload and independent monitor link','reload fetches module successfully and QR dialog opens');
  }
  report.passed=true;console.log(JSON.stringify({passed:report.passed,checks:report.checks}));
}catch(e){report.error=e.stack;process.exitCode=1;console.error(e.message);}
finally{await fs.writeFile(path.join(root,'artifacts/reports',baseline?'panel-recovery-baseline.json':'panel-recovery-browser.json'),JSON.stringify(report,null,2)+'\n');await browser.close();}
