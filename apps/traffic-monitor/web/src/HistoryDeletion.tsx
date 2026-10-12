import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Checkbox, DatePicker, Descriptions, Modal, Space } from 'antd';
import type { Dayjs } from 'dayjs';
import { APIError, deleteHistory, fetchAPI } from './api';
import { chinaNow, dateBounds, formatBytes, formatTime, rangeDays } from './utils';
import type { Client, HistoryPreview } from './types';
import { ClientPicker, clientLabel } from './ClientPicker';

export function HistoryDeletion({ clients, initialClient, initialRange, onClose, onDeleted, onAuthRequired }: { clients: Client[]; initialClient?: string; initialRange: [Dayjs, Dayjs]; onClose: () => void; onDeleted: () => void; onAuthRequired: () => void }) {
  const [client, setClient] = useState(initialClient || '');
  const [range, setRange] = useState(initialRange);
  const [preview, setPreview] = useState<HistoryPreview>();
  const [agreed, setAgreed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const reset = () => { controller.current?.abort(); setPreview(undefined); setAgreed(false); setError(''); setBusy(false); };
  const run = async (remove: boolean) => {
    if (rangeDays(range) > 35 || range[1].isBefore(range[0], 'minute')) { setError('请选择先后顺序正确且不超过 35 天的时间区间'); return; }
    controller.current?.abort();
    const request = new AbortController(); controller.current = request;
    setBusy(true); setError('');
    try {
      if (remove && preview) { await deleteHistory(preview.preview_token, request.signal); onDeleted(); }
      else {
        const params = new URLSearchParams(dateBounds(range));
        if (client) params.set('client_id', client);
        setPreview(await fetchAPI<HistoryPreview>('history/preview', params, request.signal));
        setAgreed(false);
      }
    } catch (failure) {
      if (request.signal.aborted) return;
      if (failure instanceof APIError && failure.status === 401) onAuthRequired();
      else { setError(failure instanceof Error ? failure.message : '操作失败，请重试'); setPreview(undefined); setAgreed(false); }
    } finally { if (!request.signal.aborted) setBusy(false); }
  };
  const hasData = !!preview && (preview.visit_count > 0 || preview.summary_visit_count > 0 || preview.traffic_buckets > 0);
  return <Modal open title="删除日志与监测流量" onCancel={() => { if (!busy) onClose(); }} closable={!busy} maskClosable={!busy} width={680} footer={<Space><Button onClick={onClose} disabled={busy}>取消</Button><Button onClick={() => run(false)} loading={busy} disabled={busy}>预览删除范围</Button><Button danger type="primary" onClick={() => run(true)} loading={busy} disabled={!preview || !agreed || !hasData || busy}>确认删除</Button></Space>}>
    <div className="deletion-content">
      <Alert type="warning" showIcon title="同时删除访问记录、访问次数和采样流量" description="删除范围包含所选客户端在该时段的全部目标和入站。单条访问日志没有独立字节数，因此不能只扣除某个域名的流量。此操作无法撤销。" />
      <ClientPicker title="删除哪些客户端" ariaLabel="删除哪些客户端" clients={clients} value={client} disabled={busy} onChange={value => { reset(); setClient(value || ''); }} allLabel="所有客户端（包含历史及未匹配记录）" />
      <label className="filter-field"><span>删除时间范围（北京时间，包含结束分钟）</span><DatePicker.RangePicker value={range} disabled={busy} allowClear={false} format="YYYY-MM-DD HH:mm" showTime={{ format: 'HH:mm' }} needConfirm={false} disabledDate={date => date.isAfter(chinaNow(), 'day')} onChange={dates => { if (dates?.[0] && dates[1]) { reset(); setRange([dates[0].startOf('minute'), dates[1].startOf('minute')]); } }} /></label>
      {error && <Alert type="error" showIcon title={error} />}
      {preview && <>
        <Descriptions bordered size="small" column={2} items={[
          { key: 'range', label: '实际删除范围', span: 2, children: `${formatTime(preview.start)} 至 ${formatTime(preview.end)}（不含结束时刻）` },
          { key: 'client', label: '客户端范围', span: 2, children: client ? (() => { const selected = clients.find(c => c.id === client); return selected ? clientLabel(selected) : client; })() : '所有客户端 · 所有目标 · 所有入站' },
          { key: 'clients', label: '有记录的客户端', children: `${preview.client_count} 个` },
          { key: 'visits', label: '逐条访问明细', children: `${preview.visit_count} 条` },
          { key: 'counts', label: '汇总访问次数', children: `${preview.summary_visit_count} 次` },
          { key: 'buckets', label: '分钟用量记录', children: `${preview.traffic_buckets} 条` },
          { key: 'up', label: '上传', children: formatBytes(preview.up) },
          { key: 'down', label: '下载', children: formatBytes(preview.down) },
          { key: 'total', label: '将扣除的监测流量', span: 2, children: <strong>{formatBytes(preview.total)}</strong> },
        ]} />
        <Alert type="info" showIcon title={hasData ? '确认后，所选日期的流量卡片、趋势和目标汇总会同步更新。' : '该范围没有可删除的监测数据。'} description="若包含当前时段，删除截止时间按分钟冻结；该分钟内新到达的记录也将忽略，之后恢复采集。预览有效期 5 分钟，数据变化时需重新预览。" />
        <Checkbox aria-label="确认删除所选范围" checked={agreed} disabled={!hasData || busy} onChange={event => setAgreed(event.target.checked)}>我已核对范围，确认删除其中全部目标的监测记录与流量</Checkbox>
      </>}
      <p className="deletion-note">原面板累计计数、客户端限额继续由 3x-ui 管理。Xray 原始日志文件按最多 7 天的保留规则清理；已删除范围不会重新导入监测历史。</p>
    </div>
  </Modal>;
}
