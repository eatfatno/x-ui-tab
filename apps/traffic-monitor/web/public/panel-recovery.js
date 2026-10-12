/* Recovery for failed official modules; no panel bundle or security policy edits. */
(() => {
  'use strict';
  if (window.__trafficMonitorRecovery) return;
  window.__trafficMonitorRecovery = true;
  const script = document.currentScript;
  if (!script?.src) return;
  const monitor = new URL('./', script.src);
  if (monitor.origin !== location.origin) return;
  const assetPrefix = monitor.pathname.replace(/traffic-monitor\/$/, 'assets/');
  const moduleFailure = /Failed to fetch dynamically imported module|Importing a module script failed|error loading dynamically imported module|Unable to preload CSS/i;
  let pending = false;
  function showRecovery() {
    if (pending) return;
    pending = true;
    function mount() {
      const box = document.createElement('section');
      box.id = 'traffic-monitor-panel-recovery';
      box.setAttribute('role', 'alert');
      box.style.cssText = 'position:fixed;z-index:2147483647;inset:16px 16px auto;margin:auto;max-width:620px;padding:20px;border:1px solid #91caff;border-radius:10px;background:#fff;color:#17233b;box-shadow:0 6px 24px #0003;font:15px/1.7 system-ui,sans-serif';
      const title = document.createElement('strong');
      title.textContent = '面板页面加载中断';
      const text = document.createElement('p');
      text.textContent = '部分页面资源未能加载，可能是连接中断或面板更新。请重新加载后再打开刚才的功能；未保存的编辑会丢失。';
      const retry = document.createElement('button');
      retry.type = 'button';
      retry.textContent = '重新加载页面';
      retry.style.cssText = 'padding:7px 16px;border:0;border-radius:6px;background:#1677ff;color:white;font:inherit;cursor:pointer';
      retry.addEventListener('click', () => location.reload());
      const link = document.createElement('a');
      link.href = monitor.href;
      link.textContent = '打开流量监测';
      link.style.cssText = 'margin-left:18px;color:#0958d9';
      box.append(title, text, retry, link);
      document.body.appendChild(box);
      retry.focus();
    }
    if (document.body) mount();
    else document.addEventListener('DOMContentLoaded', mount, { once: true });
  }
  window.addEventListener('vite:preloadError', event => {
    if (!moduleFailure.test(String(event.payload?.message || event.payload || ''))) return;
    // Keep the official one-shot reload for stale bundles. If its session-wide
    // allowance is already spent, this control still lets the user recover.
    // Do not preventDefault: Vite would resolve the failed import to undefined.
    showRecovery();
  }, true);
  window.addEventListener('error', event => {
    const source = event.target?.tagName === 'SCRIPT' ? event.target.src : '';
    if (!source) return;
    const url = new URL(source, location.href);
    if (url.origin === location.origin && url.pathname.startsWith(assetPrefix)) showRecovery();
  }, true);
  window.addEventListener('unhandledrejection', event => {
    if (moduleFailure.test(String(event.reason?.message || ''))) showRecovery();
  });
})();
