import dayjs, { type Dayjs } from 'dayjs';
import utc from 'dayjs/plugin/utc';
import timezone from 'dayjs/plugin/timezone';
import type { Client } from './types';
dayjs.extend(utc);
dayjs.extend(timezone);

export const TIMEZONE = 'Asia/Shanghai';
export const chinaNow = () => dayjs().tz(TIMEZONE);
export const todayRange = (): [Dayjs, Dayjs] => [chinaNow().startOf('day'), chinaNow().endOf('day').startOf('minute')];

export function activeClientCount(clients: Client[], clientID?: string, inboundID?: number): number {
  return clients.filter(client => !client.deleted && (!clientID || client.id === clientID) && (inboundID === undefined || client.inbound_ids.includes(inboundID))).length;
}

// Wall-clock selections are interpreted in Shanghai, independent of browser zone.
// The chosen final minute is included; APIs continue to use [start,end).
export function dateBounds(range: [Dayjs, Dayjs]) {
  const start = dayjs.tz(range[0].format('YYYY-MM-DD HH:mm'), TIMEZONE).startOf('minute');
  const end = dayjs.tz(range[1].format('YYYY-MM-DD HH:mm'), TIMEZONE).startOf('minute').add(1, 'minute');
  return { start: start.toISOString(), end: end.toISOString() };
}
export function rangeDays(range: [Dayjs, Dayjs]) {
  const { start, end } = dateBounds(range);
  return (Date.parse(end) - Date.parse(start)) / 86400000;
}
export function formatBytes(value?: number, digits = 2): string {
  if (value == null || !Number.isFinite(value)) return '—';
  const safe = Math.max(0, value);
  if (safe < 1024) return `${Math.round(safe)} B`;
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  const exponent = Math.min(Math.floor(Math.log(safe) / Math.log(1024)), units.length - 1);
  return `${(safe / 1024 ** exponent).toFixed(digits)} ${units[exponent]}`;
}
export function formatTime(value?: string, short = false): string {
  if (!value || !dayjs(value).isValid()) return '尚未采集';
  return dayjs(value).tz(TIMEZONE).format(short ? 'MM-DD HH:mm' : 'YYYY-MM-DD HH:mm:ss');
}
export function panelURL(): string {
  const path = window.location.pathname;
  const marker = path.indexOf('/traffic-monitor');
  return `${marker >= 0 ? path.slice(0, marker) : ''}/panel/`;
}
export const qualityLabels: Record<string, string> = {
  before_monitoring: '所选范围包含接入前时段，无法提供此前历史数据',
  detail_expired: '部分访问明细已超过 7 天保留期，仍可查看有效汇总',
  summary_expired: '部分汇总已超过 35 天保留期',
  collection_gap: '所选范围存在采集缺口，用量可能不完整',
  counter_reset: '检测到累计计数重置，相关时段已标记',
  counter_reset_window: '面板累计清零操作期间已重建采样基线，该交接时段用量可能不完整',
  unmatched_identity: '部分访问记录未能匹配到客户端身份',
  sampling: '流量按累计值差额采样，通常每 10 秒更新',
  inbound_client_totals: '入站筛选的流量为关联客户端合计（包含该客户端的其他入站）',
  log_parse_error: '部分访问日志无法解析，访问统计可能不完整',
  sample_stale: '尚未收到最近的流量采样',
  adapter_unsupported: '当前面板版本尚未验证布局兼容性',
  panel_collection_failed: '暂时无法从面板同步客户端流量',
  online_collection_failed: '暂时无法从面板同步在线状态',
  access_log_unavailable: '访问日志暂不可读取',
  log_backlog: '访问日志正在分批追赶，近期访问统计暂未完整同步',
  database_cleanup_failed: '过期数据清理失败，请检查存储状态',
  log_rotation_failed: '访问日志轮转失败，请检查日志状态',
  panel_metadata_failed: '官方版本或入站信息同步失败',
  log_truncated: '访问日志发生截断，相关时段可能存在记录缺口',
  history_deleted: '所选范围内曾清理监测历史，已删除部分不计入当前统计',
};
