import { useEffect, useRef, useState } from 'react';
import { Alert, Badge, Card, Empty, Space, Switch, Table, Tooltip } from 'antd';
import { ThunderboltOutlined } from '@ant-design/icons';
import { APIError, fetchAPI } from './api';
import { formatBytes, formatTime } from './utils';
import type { LiveClient, LiveSnapshot } from './types';

const rateLabels: Record<string, string> = {
  baseline: '正在建立采样基线', reset: '累计刚清零或发生回退，等待下一轮采样',
  gap: '采样间隔过长，本轮不计算速度', stale: '采样已过时，等待恢复',
};

export function RealtimeClients({ client, inbound, auto, paused, revision, onAuthRequired }: {
  client?: string; inbound?: number; auto: boolean; paused: boolean; revision: number; onAuthRequired: () => void;
}) {
  const [snapshot, setSnapshot] = useState<LiveSnapshot>();
  const [error, setError] = useState('');
  const [onlyOnline, setOnlyOnline] = useState(true);
  const auth = useRef(onAuthRequired); auth.current = onAuthRequired;
  useEffect(() => {
    if (paused) return;
    const controller = new AbortController();
    let timer: number | undefined;
    setSnapshot(undefined); setError('');
    const params = new URLSearchParams();
    if (client) params.set('client_id', client);
    if (inbound !== undefined) params.set('inbound_id', String(inbound));
    const refresh = async () => {
      let interval = 10;
      let unauthorized = false;
      try {
        if (!document.hidden) {
          const next = await fetchAPI<LiveSnapshot>('realtime', params, controller.signal);
          if (controller.signal.aborted) return;
          setSnapshot(next); setError(''); interval = Math.max(10, next.interval_seconds);
        }
      } catch (failure) {
        if (controller.signal.aborted) return;
        if (failure instanceof APIError && failure.status === 401) { unauthorized = true; auth.current(); }
        else { setError(failure instanceof Error ? failure.message : '实时状态暂不可用'); setSnapshot(undefined); }
      }
      if (auto && !controller.signal.aborted && !unauthorized) timer = window.setTimeout(refresh, interval * 1000);
    };
    void refresh();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [client, inbound, auto, paused, revision]);

  const rows = (snapshot?.items || []).filter(row => !onlyOnline || row.status !== 'offline');
  const online = (snapshot?.items || []).filter(row => row.status === 'online').length;
  const rate = (value: number | null, row: LiveClient) => value === null ? <Tooltip title={rateLabels[row.rate_state] || '暂无采样'}><span className="live-unavailable">—</span></Tooltip> : <strong className="tabular">{formatBytes(value)}/s</strong>;
  const amount = (value: number | null, row: LiveClient) => value === null ? <Tooltip title={rateLabels[row.rate_state] || '暂无采样'}>—</Tooltip> : <span className="tabular">{formatBytes(value)}</span>;
  const columns = [
    { title: '客户端', dataIndex: 'email', width: 220, ellipsis: true },
    { title: '状态', dataIndex: 'status', width: 112, render: (status: string) => <Badge status={status === 'online' ? 'success' : status === 'offline' ? 'default' : 'warning'} text={status === 'online' ? '在线' : status === 'offline' ? '离线' : '状态未知'} /> },
    { title: '上传速度', dataIndex: 'up_per_second', width: 160, render: rate },
    { title: '下载速度', dataIndex: 'down_per_second', width: 160, render: rate },
    { title: '本轮上传', dataIndex: 'up', width: 150, render: amount },
    { title: '本轮下载', dataIndex: 'down', width: 150, render: amount },
  ];
  return <Card variant="borderless" className="realtime-card" title={<span><ThunderboltOutlined /> 实时在线客户端</span>} extra={<Space wrap><span>{snapshot?.online_available && !snapshot.stale ? `${online} 个在线` : '在线状态待同步'}</span><Space size={6}><span>仅显示在线</span><Switch size="small" checked={onlyOnline} onChange={setOnlyOnline} aria-label="仅显示在线客户端" /></Space></Space>}>
    <p className="realtime-caption">最近一轮采样的平均速度及上传/下载量，约每 {snapshot?.interval_seconds || 10} 秒更新。按客户端统计全部连接，不受日期或请求目标筛选影响。</p>
    {error && <Alert type="error" showIcon title={error} />}
    {snapshot?.stale && <Alert type="warning" showIcon title="实时采样暂未就绪或已过时，当前无法判断在线状态和速度。" />}
    {snapshot && !snapshot.stale && !snapshot.online_available && <Alert type="warning" showIcon title="在线状态暂不可用，流量采样继续显示。" />}
    <Table<LiveClient> rowKey="id" className="realtime-table" size="small" columns={columns} dataSource={rows} scroll={{ x: 960 }} pagination={rows.length > 10 ? { pageSize: 10, showSizeChanger: false } : false} loading={!snapshot && !error && !paused} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={error ? '暂时无法读取实时数据' : snapshot?.stale ? '等待采样恢复' : onlyOnline ? '当前没有在线客户端' : '当前筛选下没有客户端'} /> }} />
    <div className="realtime-foot"><span>最近更新 {formatTime(snapshot?.updated_at)}</span><span>{snapshot && snapshot.sample_seconds > 0 && !snapshot.stale ? `本轮间隔 ${snapshot.sample_seconds.toFixed(1)} 秒 · ` : ''}在线状态跟随原面板，短连接关闭后可能延迟一个采样周期更新</span></div>
  </Card>;
}
