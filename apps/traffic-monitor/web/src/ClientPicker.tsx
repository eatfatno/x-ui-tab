import { useEffect, useState } from 'react';
import { Checkbox, Select, Tooltip } from 'antd';
import type { Client } from './types';
import { formatTime } from './utils';

export function clientLabel(client: Client) {
  if (!client.deleted) return client.email || client.id;
  const identity = client.id.split(':').at(-2) || client.id.slice(-6);
  return `${client.email || client.id}（已删除 · ${formatTime(client.last_seen, true)} · #${identity}）`;
}

export function ClientPicker({ clients, value, onChange, title, ariaLabel, disabled, className = '', allLabel }: {
  clients: Client[]; value?: string; onChange: (value?: string) => void; title: string; ariaLabel: string;
  disabled?: boolean; className?: string; allLabel?: string;
}) {
  const [history, setHistory] = useState(() => clients.some(c => c.id === value && c.deleted));
  useEffect(() => {
    if (clients.some(c => c.id === value && c.deleted)) setHistory(true);
  }, [clients, value]);
  const archived = clients.some(c => c.deleted);
  const visible = clients.filter(c => !c.deleted || history || c.id === value);
  const options = visible.map(c => ({ value: c.id, label: clientLabel(c) }));
  if (allLabel) options.unshift({ value: '', label: allLabel });
  return <div className={`filter-field client-picker ${className}`}>
    <div className="client-picker-heading"><span>{title}</span>{archived && <Tooltip title="查看仍有保留记录的已删除客户端；此开关不改变“所有客户端”的范围。关闭时清除所选历史客户端。">
      <Checkbox disabled={disabled} checked={history} onChange={event => {
        setHistory(event.target.checked);
        if (!event.target.checked && clients.some(c => c.id === value && c.deleted)) onChange(allLabel ? '' : undefined);
      }}>显示历史客户端</Checkbox>
    </Tooltip>}</div>
    <Select aria-label={ariaLabel} allowClear={!allLabel} placeholder="全部客户端" value={value} showSearch optionFilterProp="label" disabled={disabled} onChange={onChange} options={options} />
  </div>;
}
