#!/usr/bin/env python3
"""Read-only WSL process profile for this workspace's panel, Xray and monitor."""
import argparse
import json
import os
from pathlib import Path
import time

ROOT = Path(__file__).resolve().parents[1]


def read_process(pid):
    folder = Path('/proc') / str(pid)
    # Do not read command lines or environment; they can contain credentials.
    executable = (folder / 'exe').resolve()
    if not executable.is_relative_to(ROOT / 'runtime'):
        return None
    fields = (folder / 'stat').read_text().split(') ', 1)[1].split()
    status = dict(line.split(':', 1) for line in (folder / 'status').read_text().splitlines() if ':' in line)
    rss = int(status['VmRSS'].split()[0]) * 1024
    return {'name': executable.name, 'pid': pid, 'cpu_ticks': int(fields[11]) + int(fields[12]), 'rss_bytes': rss}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output-name', default='resource-idle.json')
    parser.add_argument('--duration', type=int, default=30)
    parser.add_argument('--scenario', choices=['idle', 'panel-navigation'], default='idle')
    args = parser.parse_args()
    if Path(args.output_name).name != args.output_name or not args.output_name.endswith('.json'):
        parser.error('--output-name must be a JSON filename within artifacts/reports')
    if not 10 <= args.duration <= 120:
        parser.error('--duration must be between 10 and 120 seconds')
    initial = {}
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit():
            continue
        try:
            value = read_process(int(proc.name))
            if value:
                initial[value['pid']] = value
        except (OSError, KeyError, ValueError):
            pass
    if len(initial) != 3:
        raise RuntimeError('Expected only the live panel, monitor and Xray; stop disposable fixtures first')
    started = time.monotonic()
    peaks = {pid: v['rss_bytes'] for pid, v in initial.items()}
    peak_cpu = {pid: 0 for pid in initial}
    previous, previous_at = initial, started
    peak_total_rss, peak_total_cpu = 0, 0
    for _ in range(args.duration):
        time.sleep(1)
        current = {pid: read_process(pid) for pid in initial}
        at = time.monotonic()
        total_cpu = 0
        for pid in initial:
            peaks[pid] = max(peaks[pid], current[pid]['rss_bytes'])
            percent = (current[pid]['cpu_ticks'] - previous[pid]['cpu_ticks']) / os.sysconf('SC_CLK_TCK') / (at - previous_at) * 100
            peak_cpu[pid] = max(peak_cpu[pid], percent)
            total_cpu += percent
        peak_total_cpu = max(peak_total_cpu, total_cpu)
        peak_total_rss = max(peak_total_rss, sum(v['rss_bytes'] for v in current.values()))
        previous, previous_at = current, at
    elapsed = time.monotonic() - started
    values = []
    for pid, before in initial.items():
        after = read_process(pid)
        seconds = (after['cpu_ticks'] - before['cpu_ticks']) / os.sysconf('SC_CLK_TCK')
        values.append({'name': after['name'], 'cpu_percent_of_one_core': seconds / elapsed * 100,
                       'rss_bytes': after['rss_bytes'], 'peak_rss_bytes': peaks[pid], 'peak_cpu_percent_one_core': peak_cpu[pid]})
    report = {'scenario': args.scenario, 'duration_seconds': elapsed, 'processes': values, 'total_rss_bytes': sum(x['rss_bytes'] for x in values),
              'peak_total_rss_bytes': peak_total_rss, 'peak_total_cpu_percent_one_core': peak_total_cpu,
              'note': 'Live WSL processes, collector enabled; no CPU affinity restriction. Excludes OS, browser, page cache and Xray transfer load. RSS sum can include shared pages; peak CPU uses one-second windows.'}
    output = ROOT / 'artifacts/reports' / args.output_name
    output.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
