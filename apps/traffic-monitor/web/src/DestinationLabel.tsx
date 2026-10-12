import { Tooltip } from 'antd';
import type { Destination } from './types';

export function DestinationLabel({ destination, raw }: { destination: Destination; raw: string }) {
  const named = destination.kind === 'service' || destination.kind === 'service_ip';
  return <div className="destination-label">
    <Tooltip title={named ? `${destination.kind === 'service_ip' ? '官方专属 IP 网段' : '域名规则'}匹配：${destination.name}；原始目标：${raw}` : raw}>
      <span className={named ? 'service-name' : 'target-cell'}>{destination.name || raw || '—'}</span>
    </Tooltip>
    {named && <span className="destination-raw" title={raw}>{raw}</span>}
  </div>;
}
