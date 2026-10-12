import { useEffect, useId, useRef, useState } from 'react';
import { Empty } from 'antd';
import type { TrafficPoint } from './types';
import { formatBytes, formatTime } from './utils';

export function TrafficChart({ points }: { points: TrafficPoint[] }) {
  const id = useId().replace(/:/g, '');
  const container = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 800, height: 210 });
  useEffect(() => {
    const node = container.current;
    if (!node) return;
    const observer = new ResizeObserver(entries => {
      const { width, height } = entries[0].contentRect;
      const next = { width: Math.max(240, Math.round(width)), height: Math.max(170, Math.round(height)) };
      setSize(previous => previous.width === next.width && previous.height === next.height ? previous : next);
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  if (!points.length) return <div ref={container} className="chart-empty"><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="所选时段暂无可用流量采样" /></div>;
  const { width, height } = size;
  const left = width < 500 ? 64 : 74, right = 14, top = 20, bottom = 34;
  const innerW = width - left - right, innerH = height - top - bottom;
  const max = Math.max(...points.map(p => Math.max(p.up, p.down)), 1) * 1.12;
  const minTime = Date.parse(points[0].time), maxTime = Date.parse(points[points.length - 1].time);
  const x = (p: TrafficPoint) => maxTime === minTime ? left + innerW / 2 : left + (Date.parse(p.time) - minTime) / (maxTime - minTime) * innerW;
  const y = (v: number) => top + innerH * (1 - v / max);
  const path = (key: 'up' | 'down') => points.map((p, i) => `${i ? 'L' : 'M'}${x(p)},${y(p[key])}`).join(' ');
  const area = (key: 'up' | 'down') => `${path(key)} L${x(points[points.length - 1])},${top + innerH} L${x(points[0])},${top + innerH} Z`;
  const tickIndices = [...new Set(width < 500 ? [0, points.length - 1] : [0, Math.floor((points.length - 1) / 2), points.length - 1])];
  return <div ref={container}><svg viewBox={`0 0 ${width} ${height}`} className="traffic-chart" role="img" aria-label="上传与下载流量趋势；单位为每个采样汇总时段的字节量">
    <defs>
      <linearGradient id={`${id}-down`} x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="#1677ff" stopOpacity=".2" /><stop offset="100%" stopColor="#1677ff" stopOpacity=".01" /></linearGradient>
      <linearGradient id={`${id}-up`} x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="#27aa9b" stopOpacity=".12" /><stop offset="100%" stopColor="#27aa9b" stopOpacity=".01" /></linearGradient>
    </defs>
    {[0, 1, 2, 3, 4].map(i => <g key={i}><line x1={left} x2={width - right} y1={y(max * i / 4)} y2={y(max * i / 4)} className="chart-grid" /><text x={left - 12} y={y(max * i / 4) + 4} textAnchor="end" className="chart-label">{formatBytes(max * i / 4, 1)}</text></g>)}
    <path d={area('down')} fill={`url(#${id}-down)`} /><path d={area('up')} fill={`url(#${id}-up)`} />
    <path d={path('down')} fill="none" stroke="#1677ff" strokeWidth="2.5" strokeLinejoin="round" /><path d={path('up')} fill="none" stroke="#27aa9b" strokeWidth="2.3" strokeLinejoin="round" />
    {points.length === 1 && <><circle cx={x(points[0])} cy={y(points[0].down)} r="4" fill="#1677ff" /><circle cx={x(points[0])} cy={y(points[0].up)} r="4" fill="#27aa9b" /></>}
    {tickIndices.map(i => <text key={i} x={x(points[i])} y={height - 11} textAnchor={i === 0 ? 'start' : i === points.length - 1 ? 'end' : 'middle'} className="chart-label">{formatTime(points[i].time, true)}</text>)}
    {points.map((p, i) => <rect key={`${p.time}-${i}`} x={x(p) - 5} y={top} width="10" height={innerH} fill="transparent"><title>{formatTime(p.time)} · 上传 {formatBytes(p.up)} · 下载 {formatBytes(p.down)}</title></rect>)}
  </svg></div>;
}
