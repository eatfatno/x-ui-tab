import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import { afterEach, describe, expect, it } from 'vitest';

const script = readFileSync(new URL('../public/adapter.js', import.meta.url), 'utf8');
let dom: JSDOM | undefined;
afterEach(() => dom?.window.close());

function setup(version = '3.9.0', selected = true, shape = true) {
  dom = new JSDOM(`<!doctype html><html><head></head><body><div class="index-page"><div class="ant-sidebar"><ul class="sider-nav"><li class="ant-menu-item">Dashboard</li><li class="ant-menu-item">Inbound</li><li class="ant-menu-item">Clients</li></ul></div><section class="content-shell"><main class="content-area">${shape ? '<div class="ov-page"><div class="ov-bar">Original header</div><div class="ov-vitals">Original cards</div></div>' : 'Unknown page'}</main></section></div></body></html>`, { url: `https://panel.test/secret/panel/${selected ? '?monitor=traffic' : ''}`, runScripts: 'outside-only', pretendToBeVisual: true });
  const window = dom.window;
  const tag = window.document.createElement('script');
  tag.src = 'https://panel.test/secret/traffic-monitor/adapter.js';
  Object.defineProperty(window.document, 'currentScript', { value: tag });
  Object.assign(window, { X_UI_CUR_VER: version });
  window.eval(script);
  return window;
}

describe('verified official layout integration', () => {
  it('adds one menu, preserves header, and embeds under the actual secret base', () => {
    const window = setup();
    expect(window.document.querySelectorAll('.tm-menu-item')).toHaveLength(1);
    expect(window.document.querySelector('.ov-bar')?.textContent).toBe('Original header');
    expect(window.document.querySelector<HTMLIFrameElement>('#traffic-monitor-frame')?.src).toBe('https://panel.test/secret/traffic-monitor/');
    expect(window.document.body.classList.contains('tm-active')).toBe(true);
    window.eval(script);
    expect(window.document.querySelectorAll('#traffic-monitor-frame')).toHaveLength(1);
  });
  it('does not hide the panel or create an iframe on unverified versions', () => {
    const window = setup('4.0.0');
    expect(window.document.querySelector('#traffic-monitor-frame')).toBeNull();
    expect(window.document.body.classList.contains('tm-active')).toBe(false);
    expect(window.document.querySelector<HTMLAnchorElement>('#traffic-monitor-fallback')?.href).toBe('https://panel.test/secret/traffic-monitor/');
  });
  it('leaves an unknown DOM fully visible', () => {
    const window = setup('3.9.0', true, false);
    expect(window.document.querySelector('#traffic-monitor-frame')).toBeNull();
    expect(window.document.body.classList.contains('tm-active')).toBe(false);
  });
  it('does not show monitor content until the tab is selected', () => {
    const window = setup('3.9.0', false);
    expect(window.document.querySelector('#traffic-monitor-frame')).toBeNull();
    window.document.querySelector<HTMLElement>('.tm-menu-item')!.click();
    expect(window.location.search).toBe('?monitor=traffic');
    expect(window.document.querySelector('#traffic-monitor-frame')).not.toBeNull();
  });
});
