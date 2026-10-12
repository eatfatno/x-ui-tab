import { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Badge, Button, Card, ConfigProvider, DatePicker, Empty, Input, Result, Select, Space, Spin, Switch, Table, Tabs, Tag, Tooltip, theme } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { ArrowDownOutlined, ArrowLeftOutlined, ArrowUpOutlined, CheckCircleOutlined, ClockCircleOutlined, ExportOutlined, GlobalOutlined, LineChartOutlined, ReloadOutlined, SwapOutlined } from '@ant-design/icons';
import dayjs, { type Dayjs } from 'dayjs';
import 'dayjs/locale/zh-cn';
import { APIError, fetchAPI } from './api';
import { TrafficChart } from './TrafficChart';
import { DestinationLabel } from './DestinationLabel';
import { TargetsTable } from './TargetsTable';
import { HistoryDeletion } from './HistoryDeletion';
import { RealtimeClients } from './RealtimeClients';
import { ClientPicker } from './ClientPicker';
import { chinaNow, dateBounds, formatBytes, formatTime, panelURL, qualityLabels, rangeDays, todayRange } from './utils';
import type { Client, Health, Summary, Targets, TrafficPoint, Visit, Visits } from './types';

dayjs.locale('zh-cn');
type Filters = { range: [Dayjs, Dayjs]; client?: string; inbound?: number; target: string };
type Dataset = { clients: Client[]; summary: Summary; traffic: TrafficPoint[]; visits?: Visits; targets?: Targets; health: Health; rules: { service_count: number; rule_count: number; ip_rule_count: number; commit: string; app_rule_commit: string } };
const EMPTY_PARAMS = new URLSearchParams();
const embedded = window.parent !== window;

function initialTheme() {
  try { return localStorage.getItem('dark-mode') !== 'false'; } catch { return true; }
}

export function App() {
  const [reducedMotion, setReducedMotion] = useState(() => window.matchMedia('(prefers-reduced-motion: reduce)').matches);
  const [dark, setDark] = useState(initialTheme);
  const [ultra, setUltra] = useState(() => { try { return localStorage.getItem('isUltraDarkThemeEnabled') === 'true'; } catch { return false; } });
  const [draft, setDraft] = useState<Filters>(() => ({ range: todayRange(), target: '' }));
  const [filters, setFilters] = useState(draft);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [targetPage, setTargetPage] = useState(1);
  const [targetPageSize, setTargetPageSize] = useState(25);
  const [activeTab, setActiveTab] = useState('targets');
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deletedNotice, setDeletedNotice] = useState(false);
  const [revision, setRevision] = useState(0);
  const [auto, setAuto] = useState(true);
  const [loading, setLoading] = useState(true);
  const [data, setData] = useState<Dataset | null>(null);
  const [error, setError] = useState('');
  const [dateError, setDateError] = useState('');
  const [unauthorized, setUnauthorized] = useState(false);
  const [updatedAt, setUpdatedAt] = useState('');
  const contentRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const query = window.matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setReducedMotion(query.matches);
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);

  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== window.location.origin || event.source !== window.parent || !event.data) return;
      if (event.data.type === 'traffic-monitor:theme') { setDark(Boolean(event.data.dark)); setUltra(Boolean(event.data.ultra)); }
    };
    window.addEventListener('message', onMessage);
    if (embedded) window.parent.postMessage({ type: 'traffic-monitor:ready' }, window.location.origin);
    return () => window.removeEventListener('message', onMessage);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = dark ? (ultra ? 'ultra-dark' : 'dark') : 'light';
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
  }, [dark, ultra]);

  useEffect(() => {
    if (!embedded || !contentRef.current) return;
    let pending = 0;
    const sendSize = () => {
      cancelAnimationFrame(pending);
      pending = requestAnimationFrame(() => window.parent.postMessage({ type: 'traffic-monitor:resize', height: Math.ceil(contentRef.current!.getBoundingClientRect().height) + 16 }, window.location.origin));
    };
    const observer = new ResizeObserver(sendSize);
    observer.observe(contentRef.current);
    sendSize();
    return () => { observer.disconnect(); cancelAnimationFrame(pending); };
  }, [unauthorized]);

  useEffect(() => {
    if (!auto || unauthorized || loading || deleteOpen) return;
    const interval = window.setInterval(() => {
      if (!document.hidden) setRevision(value => value + 1);
    }, 30000);
    return () => clearInterval(interval);
  }, [auto, unauthorized, loading, deleteOpen]);

  useEffect(() => {
    if (unauthorized) return;
    const controller = new AbortController();
    const params = new URLSearchParams(dateBounds(filters.range));
    if (filters.client) params.set('client_id', filters.client);
    if (filters.inbound !== undefined) params.set('inbound_id', String(filters.inbound));
    const visitParams = new URLSearchParams(params);
    visitParams.set('page', String(page));
    visitParams.set('page_size', String(pageSize));
    if (filters.target.trim()) visitParams.set('target', filters.target.trim());
    const targetParams = new URLSearchParams(visitParams);
    targetParams.set('page', String(targetPage)); targetParams.set('page_size', String(targetPageSize));
    setLoading(true);
    Promise.all([
      fetchAPI<Client[]>('clients', EMPTY_PARAMS, controller.signal),
      fetchAPI<Summary>('summary', params, controller.signal),
      fetchAPI<TrafficPoint[]>('traffic', params, controller.signal),
      activeTab === 'visits' ? fetchAPI<Visits>('visits', visitParams, controller.signal) : Promise.resolve(null),
      fetchAPI<Health>('health', EMPTY_PARAMS, controller.signal),
      activeTab === 'targets' ? fetchAPI<Targets>('targets', targetParams, controller.signal) : Promise.resolve(null),
      fetchAPI<Dataset['rules']>('rules', EMPTY_PARAMS, controller.signal),
    ]).then(([clients, summary, traffic, visits, health, targets, rules]) => {
      if (controller.signal.aborted) return;
      setData(previous => ({ clients, summary, traffic, health, rules,
        visits: visits ?? previous?.visits,
        targets: targets ?? previous?.targets,
      }));
      setError('');
      setUpdatedAt(new Date().toISOString());
    }).catch((failure: unknown) => {
      if (controller.signal.aborted) return;
      if (failure instanceof APIError && failure.status === 401) {
        setData(null);
        setUnauthorized(true);
        if (embedded) window.parent.postMessage({ type: 'traffic-monitor:auth-required' }, window.location.origin);
      } else setError(failure instanceof Error ? failure.message : '无法连接监测服务');
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [filters, page, pageSize, targetPage, targetPageSize, revision, unauthorized, activeTab]);

  const inbounds = useMemo(() => [...new Set((data?.clients || []).flatMap(c => c.inbound_ids || []))].sort((a, b) => a - b), [data?.clients]);
  const clients = useMemo(() => (data?.clients || []).filter(c => draft.inbound === undefined || c.inbound_ids?.includes(draft.inbound)), [data?.clients, draft.inbound]);
  useEffect(() => {
    if (!data) return;
    const exists = (id: string) => data.clients.some(c => c.id === id);
    if (draft.client && !exists(draft.client)) setDraft(previous => ({ ...previous, client: undefined }));
    if (filters.client && !exists(filters.client)) {
      setFilters(previous => ({ ...previous, client: undefined })); setPage(1); setTargetPage(1);
    }
  }, [data, draft.client, filters.client]);
  const quality = (data?.summary.quality || []).filter(code => code !== 'sampling');
  const healthy = data?.health.status === 'ok' || data?.health.status === 'healthy';
  const summaryUnavailable = !!data && Date.parse(dateBounds(filters.range).end) <= Math.max(Date.parse(data.summary.monitor_started_at) || 0, Date.parse(data.summary.summary_available_from) || 0);
  const lastVisit = data?.summary.last_visit_at;
  const rangeBounds = dateBounds(filters.range);
  const showPreviousVisit = !loading && !error && !filters.target && data?.summary.visit_count === 0 && lastVisit && (Date.parse(lastVisit) < Date.parse(rangeBounds.start) || Date.parse(lastVisit) >= Date.parse(rangeBounds.end));
  const viewLastVisit = () => {
    if (!lastVisit) return;
    const date = dayjs(lastVisit).tz('Asia/Shanghai');
    const next: Filters = { ...filters, range: [date.startOf('day'), date.endOf('day').startOf('minute')] };
    setDraft(next); setFilters(next); setData(null); setPage(1); setTargetPage(1); setDateError('');
  };
  const apply = () => {
    if (draft.range[1].isBefore(draft.range[0], 'minute') || rangeDays(draft.range) > 35) {
      setDateError('请选择先后顺序正确且不超过 35 天的时间区间'); return;
    }
    setDateError(''); setPage(1); setTargetPage(1); setData(null); setFilters({ ...draft });
  };
  const presets: { label: string; value: [Dayjs, Dayjs] }[] = [
    { label: '今天', value: todayRange() },
    { label: '最近 1 小时', value: [chinaNow().startOf('minute').subtract(59, 'minute'), chinaNow().startOf('minute')] },
    { label: '昨天', value: [chinaNow().subtract(1, 'day').startOf('day'), chinaNow().subtract(1, 'day').endOf('day').startOf('minute')] },
    { label: '最近 7 天', value: [chinaNow().subtract(6, 'day').startOf('day'), todayRange()[1]] },
    { label: '最近 35 天', value: [chinaNow().subtract(34, 'day').startOf('day'), todayRange()[1]] },
  ];

  const columns = [
    { title: '访问时间', dataIndex: 'time', key: 'time', width: 170, render: (value: string) => <span className="tabular time-cell">{formatTime(value)}</span> },
    { title: '客户端', dataIndex: 'email', key: 'email', width: 180, ellipsis: true, render: (value: string, row: Visit) => <span title={value || row.client_id}>{value || row.client_id || '未匹配身份'}</span> },
    { title: '请求目标', dataIndex: 'target', key: 'target', width: 270, render: (value: string, row: Visit) => <DestinationLabel destination={row.destination} raw={value} /> },
    { title: '端口', dataIndex: 'port', key: 'port', width: 76, render: (value: number) => value || '—' },
    { title: '协议', dataIndex: 'protocol', key: 'protocol', width: 86, render: (value: string) => <Tag bordered={false}>{value?.toUpperCase() || '—'}</Tag> },
    { title: '入站', dataIndex: 'inbound', key: 'inbound', width: 130, ellipsis: true },
    { title: '状态', dataIndex: 'status', key: 'status', width: 112, render: (value: string) => <Tooltip title={value === 'accepted' ? '代理接受了连接，不代表目标网站加载成功' : 'Xray 访问日志记录的状态'}><Tag color={value === 'accepted' ? 'success' : 'default'} bordered={false}>{value === 'accepted' ? '已接受' : value || '未知'}</Tag></Tooltip> },
  ];

  return <ConfigProvider locale={zhCN} theme={{ algorithm: dark ? theme.darkAlgorithm : theme.defaultAlgorithm, token: { motion: !reducedMotion, colorPrimary: '#1677ff', borderRadius: 10, fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "Microsoft YaHei", sans-serif', colorBgLayout: dark ? (ultra ? '#000' : '#1a1b1f') : '#e6e8ec', colorBgContainer: dark ? (ultra ? '#101013' : '#23252b') : '#ffffff' }, components: { Card: { bodyPadding: 20 }, Table: { cellPaddingBlockSM: 13 } } }}>
    <div ref={contentRef} className={`monitor-app${embedded ? ' embedded' : ''}`}>
      {unauthorized ? <Card><Result status="403" title="请先登录面板" subTitle="面板登录已过期。重新登录后即可继续查看监测数据。" extra={<Button type="primary" href={panelURL()} target={embedded ? '_top' : undefined}>前往登录</Button>} /></Card> : <>
        <header className="page-heading">
          <div className="heading-title"><span className="page-icon"><LineChartOutlined /></span><div><h1>流量监测</h1><p>按客户端查看流量趋势与访问记录</p></div></div>
          <Space size={12} wrap className="heading-actions">
            <span className="refresh-control"><span>自动刷新</span><Switch size="small" checked={auto} onChange={setAuto} aria-label="自动刷新实时状态和历史统计" /></span>
            <Button icon={<ReloadOutlined spin={loading} />} onClick={() => setRevision(v => v + 1)} loading={loading}>刷新</Button>
            <Button danger disabled={!data} onClick={() => { setDeleteOpen(true); setDeletedNotice(false); }}>删除日志与流量</Button>
            {!embedded && <Button icon={<ArrowLeftOutlined />} href={panelURL()}>返回面板</Button>}
          </Space>
        </header>

        <div className="status-strip">
          <Space size={18} wrap><Badge status={error ? 'error' : data ? (healthy ? 'success' : 'warning') : 'processing'} text={error ? '连接异常' : data ? (healthy ? '采集正常' : '采集需关注') : '正在连接'} /><span><ClockCircleOutlined /> 最近同步 <span className="tabular">{formatTime(data?.health.last_sample_at, true)}</span></span></Space>
          <Space size={8} wrap><Tag bordered={false}>明细 7 天</Tag><Tag bordered={false}>汇总 35 天</Tag><span className="timezone-label">北京时间 UTC+8</span></Space>
        </div>

        {error && <Alert type="error" showIcon title={error} description={data ? `当前显示 ${formatTime(updatedAt)} 获取的数据。恢复连接后将自动更新。` : '请检查监测服务及原面板连接，恢复后点击刷新。'} action={<Button size="small" onClick={() => setRevision(v => v + 1)}>重试</Button>} />}
        {data && !data.health.adapter_compatible && <Alert type="warning" showIcon title="当前面板版本尚未验证嵌入兼容性" description="独立监测页面与后台采集仍可使用，请更新布局适配器后恢复侧栏嵌入。" />}
        {!!data?.health.issues?.length && <Alert type="warning" showIcon title="采集状态提示" description={data.health.issues.map(issue => qualityLabels[issue] || issue).join('；')} />}

        <Card className="filter-card" variant="borderless">
          <div className="filters">
            <label className="filter-field date-field"><span>时间区间 · 精确到分钟</span><DatePicker.RangePicker value={draft.range} allowClear={false} format="YYYY-MM-DD HH:mm" showTime={{ format: 'HH:mm' }} needConfirm={false} presets={presets} disabledDate={date => date.isAfter(chinaNow(), 'day')} onChange={dates => { if (dates?.[0] && dates[1]) setDraft({ ...draft, range: [dates[0].startOf('minute'), dates[1].startOf('minute')] }); }} /></label>
            <label className="filter-field"><span>入站</span><Select aria-label="选择入站" allowClear placeholder="全部入站" value={draft.inbound} options={inbounds.map(id => ({ value: id, label: `入站 ${id}` }))} onChange={inbound => setDraft({ ...draft, inbound, client: undefined })} /></label>
            <ClientPicker className="client-field" title="客户端" ariaLabel="选择客户端" clients={clients} value={draft.client} onChange={client => setDraft({ ...draft, client })} />
            <label className="filter-field target-field"><span>请求目标 <Tooltip title="筛选目标汇总和访问明细；流量卡片与趋势展示所选客户端的全部流量"><span className="field-note">访问记录</span></Tooltip></span><Input allowClear prefix={<GlobalOutlined />} placeholder="搜索服务名、域名或 IP" value={draft.target} onChange={event => setDraft({ ...draft, target: event.target.value })} onPressEnter={apply} maxLength={256} /></label>
            <Button type="primary" onClick={apply} className="query-button">查询</Button>
          </div>
          {dateError && <div className="date-error" role="alert">{dateError}</div>}
          <div className="filter-foot"><span>北京时间；包含结束分钟，例如 09:30 至 09:30 查询这一整分钟</span><span><CheckCircleOutlined /> 首次采样建立基线，不计入接入前流量</span></div>
        </Card>

        {quality.length > 0 && <Alert className="quality-alert" type={quality.some(code => ['collection_gap', 'counter_reset', 'unmatched_identity', 'log_parse_error', 'log_truncated'].includes(code)) ? 'warning' : 'info'} showIcon title={quality.map(code => qualityLabels[code] || code).join('；')} />}

        <Spin spinning={loading && !data && !error}>
          <section className="metrics" aria-label="所选日期汇总">
            <Metric title="上传流量" value={formatBytes(summaryUnavailable ? undefined : data?.summary.up)} icon={<ArrowUpOutlined />} tone="teal" foot="客户端 → 代理出口" />
            <Metric title="下载流量" value={formatBytes(summaryUnavailable ? undefined : data?.summary.down)} icon={<ArrowDownOutlined />} tone="blue" foot="代理出口 → 客户端" />
            <Metric title="合计流量" value={formatBytes(summaryUnavailable ? undefined : data?.summary.total)} icon={<SwapOutlined />} tone="purple" foot="所选时间内上传 + 下载" />
            <Metric title="访问记录" value={data && !summaryUnavailable ? data.summary.visit_count.toLocaleString('zh-CN') : '—'} icon={<GlobalOutlined />} tone="orange" foot="代理连接记录，不等同于网页浏览量" />
          </section>

          <div className="chart-layout">
            <Card variant="borderless" className="trend-card" title={<span><LineChartOutlined /> 流量趋势</span>} extra={<Space size={16}><span className="legend"><i className="legend-up" />上传</span><span className="legend"><i className="legend-down" />下载</span></Space>}>
              <p className="chart-caption">每个时段的采样用量 · {filters.range[0].format('MM-DD HH:mm')} 至 {filters.range[1].format('MM-DD HH:mm')}</p>
              <TrafficChart points={data?.traffic || []} />
            </Card>
          </div>

          {deletedNotice && <Alert className="details-expired" type="success" showIcon title="所选范围的访问记录和监测流量已删除，统计已重新加载。" closable onClose={() => setDeletedNotice(false)} />}
          <RealtimeClients client={filters.client} inbound={filters.inbound} auto={auto} paused={deleteOpen} revision={revision} onAuthRequired={() => { setData(null); setUnauthorized(true); if (embedded) window.parent.postMessage({ type: 'traffic-monitor:auth-required' }, window.location.origin); }} />
          <Card variant="borderless" className="visits-card" title={<div className="visits-heading"><span><GlobalOutlined /> 请求目标与访问记录</span><span className="count-pill">{(activeTab === 'visits' ? data?.visits?.total : data?.targets?.total)?.toLocaleString('zh-CN') ?? '—'} {activeTab === 'visits' ? '条' : '组'}</span></div>} extra={<Tooltip title="目标汇总与访问明细均来自最近 7 天的日志；客户端流量与访问数量汇总保留 35 天"><span className="details-note">目标记录 7 天</span></Tooltip>}>
            <p className="visits-caption">同一服务的网页、手机/平板 App 和电脑软件合并展示，包含接口、媒体与下载域名。仅能识别通过本代理的连接；未知 IP 保留原值，目标流量不单独计量。</p>
            {showPreviousVisit && <Alert className="details-expired" type="info" showIcon title={`所选时间没有访问记录，最近一次访问在 ${formatTime(lastVisit)}`} action={<Button size="small" onClick={viewLastVisit}>查看最近访问当天</Button>} />}
            {data?.rules && <p className="rules-caption">本地域名规则集 · {data.rules.service_count.toLocaleString('zh-CN')} 个服务组 · {data.rules.rule_count.toLocaleString('zh-CN')} 条域名规则 · {data.rules.ip_rule_count} 条官方专属网段 <Tooltip title={`域名：v2fly/domain-list-community ${data.rules.commit}；App 补充：ios_rule_script ${data.rules.app_rule_commit}；Telegram IP：官方 CIDR。网页和 App 归入同一服务，共享云服务无法确定具体 App 时保留原归属。`}><span className="metadata-hint">识别说明</span></Tooltip></p>}
            {(activeTab === 'visits' ? data?.visits?.expired : data?.targets?.expired) && <Alert type="info" showIcon title="所选范围包含过期明细，仅展示最近 7 天内的记录；35 天内汇总继续保留。" className="details-expired" />}
            <Tabs activeKey={activeTab} onChange={setActiveTab} items={[
              { key: 'targets', label: `目标汇总${data?.targets ? `（${data.targets.total}）` : ''}`, children: <TargetsTable data={data?.targets} page={targetPage} pageSize={targetPageSize} loading={loading && !error && !!data && (data.targets?.page !== targetPage || data.targets?.page_size !== targetPageSize)} empty={error ? '暂时无法获取目标汇总' : data?.targets?.expired ? '所选时段的目标记录已过期' : data?.summary.quality.includes('before_monitoring') ? '监测接入前没有历史访问记录' : '所选条件下暂无目标记录'} onPage={(next, size) => { setTargetPage(size !== targetPageSize ? 1 : next); setTargetPageSize(size); }} /> },
              { key: 'visits', label: '访问明细', children:
            <Table<Visit> rowKey="id" size="small" columns={columns} dataSource={data?.visits?.items || []} loading={loading && !error && !!data && (data.visits?.page !== page || data.visits?.page_size !== pageSize)} scroll={{ x: 1024 }} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={error ? '暂时无法获取访问记录' : data?.visits?.expired ? '所选时段的访问明细已过期' : data?.summary.quality.includes('before_monitoring') ? '监测接入前没有历史访问记录' : '所选条件下暂无访问记录'} /> }} pagination={{ current: page, pageSize, total: data?.visits?.total || 0, showSizeChanger: true, pageSizeOptions: [25, 50, 100], showTotal: total => `共 ${total.toLocaleString('zh-CN')} 条`, onChange: (nextPage, nextSize) => { setPage(nextSize !== pageSize ? 1 : nextPage); setPageSize(nextSize); } }} />
              },
            ]} />
          </Card>
        </Spin>

        {deleteOpen && <HistoryDeletion clients={data?.clients || []} initialClient={filters.client} initialRange={filters.range} onClose={() => setDeleteOpen(false)} onDeleted={() => { setDeleteOpen(false); setDeletedNotice(true); setData(null); setPage(1); setTargetPage(1); setRevision(value => value + 1); }} onAuthRequired={() => { setDeleteOpen(false); setData(null); setUnauthorized(true); if (embedded) window.parent.postMessage({ type: 'traffic-monitor:auth-required' }, window.location.origin); }} />}
        <footer className="page-footer"><span>监测开始于 {formatTime(data?.health.monitor_started_at)} · 3X-UI {data?.health.panel_version || '—'}</span><span>{auto ? '实时状态约每 10 秒更新 · 历史统计每 30 秒刷新' : '页面刷新已暂停 · 后台继续采集'}{embedded && <a href="./" target="_blank" rel="noopener noreferrer">独立页面 <ExportOutlined /></a>}</span></footer>
      </>}
    </div>
  </ConfigProvider>;
}

function Metric({ title, value, foot, icon, tone }: { title: string; value: string; foot: string; icon: React.ReactNode; tone: string }) {
  return <Card variant="borderless" className={`metric metric-${tone}`}><div className="metric-label">{title}<span className="metric-icon">{icon}</span></div><div className="metric-value tabular">{value}</div><div className="metric-foot">{foot}</div><div className="metric-decoration" /></Card>;
}
