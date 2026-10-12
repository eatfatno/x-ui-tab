import { Empty, Table, Tooltip } from 'antd';
import { DestinationLabel } from './DestinationLabel';
import { formatTime } from './utils';
import type { Targets, TargetSummary } from './types';

export function TargetsTable({ data, page, pageSize, loading, empty, onPage }: { data?: Targets; page: number; pageSize: number; loading: boolean; empty: string; onPage: (page: number, size: number) => void }) {
  const columns = [
    { title: '客户端', dataIndex: 'email', width: 185, ellipsis: true, render: (value: string, row: TargetSummary) => <span title={value || row.client_id}>{value || row.client_id || '未匹配身份'}</span> },
    { title: '请求目标', key: 'target', width: 245, render: (_: unknown, row: TargetSummary) => <DestinationLabel destination={row.destination} raw={row.targets[0] || row.destination.domain} /> },
    { title: '访问次数', dataIndex: 'visit_count', width: 95, render: (value: number) => <strong className="tabular">{value.toLocaleString('zh-CN')}</strong> },
    { title: '目标地址', key: 'addresses', width: 110, render: (_: unknown, row: TargetSummary) => <Tooltip title={<div className="target-list">{row.targets.join('\n')}{row.target_count > row.targets.length && `\n…共 ${row.target_count} 个地址，展示前 ${row.targets.length} 个`}</div>}><span className="metadata-hint">{row.target_count} 个域名 / IP</span></Tooltip> },
    { title: '端口', dataIndex: 'ports', width: 130, render: (values: number[]) => <Tooltip title={values.join('、')}><span>{values.slice(0, 4).join('、')}{values.length > 4 ? '…' : ''}</span></Tooltip> },
    { title: '协议', dataIndex: 'protocols', width: 100, render: (values: string[]) => values.map(v => v.toUpperCase()).join(' / ') },
    { title: '入站', dataIndex: 'inbounds', width: 180, ellipsis: true, render: (values: string[]) => <span title={values.join('、')}>{values.join('、') || '—'}</span> },
    { title: '首次访问', dataIndex: 'first_seen', width: 170, render: (value: string) => <span className="time-cell tabular">{formatTime(value)}</span> },
    { title: '最近访问', dataIndex: 'last_seen', width: 170, render: (value: string) => <span className="time-cell tabular">{formatTime(value)}</span> },
  ];
  return <Table<TargetSummary> className="targets-table" rowKey="key" size="small" columns={columns} dataSource={data?.items || []} loading={loading} scroll={{ x: 1380 }} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={empty} /> }} pagination={{ current: page, pageSize, total: data?.total || 0, showSizeChanger: true, pageSizeOptions: [25, 50, 100], showTotal: total => `共 ${total.toLocaleString('zh-CN')} 组`, onChange: onPage }} />;
}
