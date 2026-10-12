import { describe, expect, it } from 'vitest';
import dayjs from 'dayjs';
import { activeClientCount, dateBounds, formatBytes, formatTime, rangeDays } from './utils';
import type { Client } from './types';

describe('Shanghai date query bounds', () => {
  it('preserves minute selection including the complete final minute', () => {
    expect(dateBounds([dayjs('2026-10-08 09:30'), dayjs('2026-10-08 09:30')])).toEqual({ start: '2026-10-08T01:30:00.000Z', end: '2026-10-08T01:31:00.000Z' });
  });
  it('handles month and year transitions without UTC date drift', () => {
    expect(dateBounds([dayjs('2026-12-31 23:59'), dayjs('2027-01-01 00:00')])).toEqual({ start: '2026-12-31T15:59:00.000Z', end: '2026-12-31T16:01:00.000Z' });
  });
  it('counts 35 inclusive dates correctly for the retention limit', () => {
    expect(rangeDays([dayjs('2026-09-04 00:00'), dayjs('2026-10-08 23:59')])).toBe(35);
    expect(rangeDays([dayjs('2026-09-03 23:59'), dayjs('2026-10-08 23:59')])).toBeGreaterThan(35);
  });
  it('renders UTC timestamps in Shanghai', () => {
    expect(formatTime('2026-10-07T16:00:00Z')).toBe('2026-10-08 00:00:00');
  });
});

describe('statistics formatting', () => {
  it('keeps no data distinct from an actual zero', () => {
    expect(formatBytes(undefined)).toBe('—');
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(1024)).toBe('1.00 KiB');
  });
  it('counts only current clients contributing to panel cumulative counters', () => {
    const clients = [
      { id: 'active-1', deleted: false, inbound_ids: [1] },
      { id: 'active-2', deleted: false, inbound_ids: [2] },
      { id: 'deleted', deleted: true, inbound_ids: [1] },
    ] as Client[];
    expect(activeClientCount(clients)).toBe(2);
    expect(activeClientCount(clients, undefined, 1)).toBe(1);
    expect(activeClientCount(clients, 'deleted')).toBe(0);
    expect(activeClientCount(clients, 'active-2')).toBe(1);
  });
});
