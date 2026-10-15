/* Verified 3x-ui 3.9.x and 3.10.2 layouts. The official application remains unmodified. */
(() => {
  'use strict';
  if (window.__trafficMonitorAdapter) return;
  window.__trafficMonitorAdapter = true;
  const script = document.currentScript;
  if (!script || !script.src) return;
  const monitorURL = new URL('./', script.src);
  if (monitorURL.origin !== window.location.origin) return;
  const basePath = monitorURL.pathname.replace(/traffic-monitor\/$/, '');
  const panelPath = `${basePath}panel/`;
  const selectedURL = new URL(panelPath, window.location.origin);
  selectedURL.searchParams.set('monitor', 'traffic');
  let frame = null;
  let scheduled = false;
  let lastTheme = '';
  let pendingSince = 0;
  const icon = '<span class="anticon ant-menu-item-icon" aria-hidden="true"><svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M4 3v17h17M7 15l4-5 4 3 6-8" stroke-linecap="round" stroke-linejoin="round"/></svg></span><span class="ant-menu-title-content">流量监测</span>';

  const style = document.createElement('style');
  style.id = 'traffic-monitor-adapter-style';
  // Inherit the upstream CSP nonce; do not relax any security policy.
  if (script.nonce) style.nonce = script.nonce;
  style.textContent = `
    .tm-menu-item { padding-inline:24px !important; user-select:none; }
    .tm-menu-item .anticon { font-size:16px; }
    .ant-menu-inline-collapsed .tm-menu-item { padding-inline:calc(50% - 8px) !important; }
    .ant-menu-inline-collapsed .tm-menu-item .ant-menu-title-content { display:none; }
    .tm-menu-item:focus-visible { outline:2px solid var(--ant-color-primary,#1677ff); outline-offset:-3px; }
    body.tm-active .ov-page > :not(.ov-bar) { display:none !important; }
    body.tm-active .index-page .content-area { flex:0 0 auto; padding-bottom:12px; }
    body.tm-active .sider-nav .ant-menu-item-selected:not(.tm-menu-item),
    body.tm-active .drawer-nav .ant-menu-item-selected:not(.tm-menu-item) { background:transparent !important; color:var(--ant-color-text) !important; }
    #traffic-monitor-frame { display:block; width:100%; min-height:480px; border:0; flex:none; background:transparent; }
    #traffic-monitor-fallback { position:fixed; bottom:18px; right:20px; z-index:1001; background:var(--ant-color-bg-container,#fff); color:var(--ant-color-primary,#1677ff); padding:10px 16px; border:1px solid var(--ant-color-border,#ddd); border-radius:8px; font:13px sans-serif; box-shadow:0 2px 12px #0002; text-decoration:none; }
  `;
  document.head.appendChild(style);

  function selected() {
    return location.pathname.replace(/\/$/, '') === panelPath.replace(/\/$/, '') && new URLSearchParams(location.search).get('monitor') === 'traffic';
  }
  function supported() {
    const version = String(window.X_UI_CUR_VER || '');
    return /^v?3\.9\.\d+$/.test(version) || /^v?3\.10\.2$/.test(version);
  }
  function sendTheme(force) {
    if (!frame || !frame.contentWindow) return;
    const dark = document.body.classList.contains('dark');
    const ultra = document.documentElement.dataset.theme === 'ultra-dark';
    const signature = `${dark}:${ultra}`;
    if (force || signature !== lastTheme) {
      frame.contentWindow.postMessage({ type: 'traffic-monitor:theme', dark, ultra }, location.origin);
      lastTheme = signature;
    }
  }
  function fallback() {
    if (!selected() || document.getElementById('traffic-monitor-fallback')) return;
    const link = document.createElement('a');
    link.id = 'traffic-monitor-fallback';
    link.href = monitorURL.href;
    link.textContent = '当前布局未适配 · 打开独立流量监测';
    document.body.appendChild(link);
  }
  function deactivate(clearFallback = true) {
    if (document.body.classList.contains('tm-active')) document.body.classList.remove('tm-active');
    if (frame) frame.remove();
    frame = null;
    lastTheme = '';
    document.querySelectorAll('.tm-menu-item').forEach(item => {
      if (item.classList.contains('ant-menu-item-selected')) item.classList.remove('ant-menu-item-selected');
      if (item.hasAttribute('aria-current')) item.removeAttribute('aria-current');
    });
    if (clearFallback) document.getElementById('traffic-monitor-fallback')?.remove();
  }
  function choose(event) {
    event.preventDefault();
    event.stopPropagation();
    event.stopImmediatePropagation();
    document.querySelector('.ant-drawer-open .drawer-close')?.click();
    if (location.pathname.replace(/\/$/, '') === panelPath.replace(/\/$/, '')) {
      if (!selected()) history.pushState(history.state, '', selectedURL);
      refresh();
    } else {
      // A real route navigation guarantees the official dashboard mounts its own header.
      location.assign(selectedURL.href);
    }
  }
  function installMenu(menu) {
    if (menu.querySelector('.tm-menu-item')) return;
    const item = document.createElement('li');
    item.className = 'ant-menu-item tm-menu-item';
    item.setAttribute('role', 'menuitem');
    item.setAttribute('tabindex', '0');
    item.setAttribute('title', '流量监测');
    item.setAttribute('aria-label', '流量监测');
    item.innerHTML = icon;
    item.addEventListener('click', choose, true);
    item.addEventListener('keydown', event => {
      if (event.key === 'Enter' || event.key === ' ') choose(event);
    }, true);
    const children = Array.from(menu.children).filter(child => child.classList.contains('ant-menu-item'));
    if (children[2]) children[2].after(item);
    else menu.appendChild(item);
  }
  function refresh() {
    scheduled = false;
    if (!supported()) {
      deactivate(!selected());
      if (document.querySelector('.ant-sidebar')) fallback();
      return;
    }
    document.querySelectorAll('.ant-sidebar .sider-nav, .ant-drawer .drawer-nav').forEach(installMenu);
    if (!selected()) { deactivate(); pendingSince = 0; return; }
    const shell = document.querySelector('.index-page > .content-shell');
    const overview = shell?.querySelector('.content-area .ov-page');
    const header = overview?.querySelector(':scope > .ov-bar');
    const menu = document.querySelector('.ant-sidebar .sider-nav');
    if (!shell || !overview || !header || !menu) {
      if (document.body.classList.contains('tm-active')) document.body.classList.remove('tm-active');
      if (frame) { frame.remove(); frame = null; }
      if (!pendingSince) pendingSince = Date.now();
      if (Date.now() - pendingSince > 15000) fallback();
      return;
    }
    pendingSince = 0;
    document.getElementById('traffic-monitor-fallback')?.remove();
    if (!document.body.classList.contains('tm-active')) document.body.classList.add('tm-active');
    document.querySelectorAll('.tm-menu-item').forEach(item => {
      if (!item.classList.contains('ant-menu-item-selected')) item.classList.add('ant-menu-item-selected');
      if (item.getAttribute('aria-current') !== 'page') item.setAttribute('aria-current', 'page');
    });
    if (!frame || !frame.isConnected || frame.parentElement !== shell) {
      frame?.remove();
      frame = document.createElement('iframe');
      frame.id = 'traffic-monitor-frame';
      frame.title = '客户端流量监测';
      frame.src = monitorURL.href;
      frame.setAttribute('referrerpolicy', 'same-origin');
      frame.addEventListener('load', () => sendTheme(true));
      shell.appendChild(frame);
    }
    sendTheme(false);
  }
  function schedule() {
    if (scheduled) return;
    scheduled = true;
    requestAnimationFrame(refresh);
  }
  const observer = new MutationObserver(schedule);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  const themeObserver = new MutationObserver(schedule);
  themeObserver.observe(document.body, { attributes: true, attributeFilter: ['class'] });
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  window.addEventListener('popstate', schedule);
  window.addEventListener('message', event => {
    if (event.origin !== location.origin || !frame || event.source !== frame.contentWindow || !event.data) return;
    if (event.data.type === 'traffic-monitor:ready') sendTheme(true);
    if (event.data.type === 'traffic-monitor:resize' && Number.isFinite(event.data.height)) {
      const height = `${Math.min(Math.max(event.data.height, 480), 30000)}px`;
      if (frame.style.height !== height) frame.style.height = height;
    }
    if (event.data.type === 'traffic-monitor:auth-required') location.assign(basePath);
  });
  // React Router changes history without firing popstate; polling also detects
  // official route remounts and gives delayed layouts a bounded fallback.
  window.setInterval(schedule, 1000);
  refresh();
})();
