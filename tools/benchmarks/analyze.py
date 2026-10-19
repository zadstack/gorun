#!/usr/bin/env python3
"""Validate complete raw benchmark artifacts and summarize independent repetitions."""
import argparse
import hashlib
import json
from pathlib import Path
from statistics import median

PRODUCTS = {'gorun': 'GoRun', 'wordpress': 'WordPress + MariaDB', 'astro-static': 'Astro static + Nginx'}

def analyze(source):
    source = Path(source)
    metadata = json.loads((source / 'metadata.json').read_text())
    if metadata.get('status') != 'complete':
        raise ValueError('Refusing to summarize an incomplete benchmark')
    fixture = source / 'fixture' / 'posts.json'
    if hashlib.sha256(fixture.read_bytes()).hexdigest() != metadata['fixture_sha256']:
        raise ValueError('Fixture checksum mismatch')
    expected = {(p, r, rate) for p in PRODUCTS for r in range(1, 4) for rate in (50, 200)}
    seen = set()
    rows = []
    for run in metadata['runs']:
        key = run['product'], run['repetition'], run['offered_rps']
        if key not in expected or key in seen:
            raise ValueError('Unexpected or duplicate run')
        seen.add(key)
        report = json.loads((source / run['report_file']).read_text())
        samples = json.loads((source / run['samples_file']).read_text())
        count = report['requests']
        if count <= 0 or not samples or sum(report['status_codes'].values()) != count:
            raise ValueError('Missing measurements or unaccounted HTTP requests')
        if report['errors'] or report['status_codes'] != {'200': count} or report['success'] != 1:
            raise ValueError('Observed HTTP failures; revise summary before publication')
        # Independently reconcile Vegeta rate and throughput; duration/wait are ns.
        actual = count / (report['duration'] / 1e9)
        throughput = count / ((report['duration'] + report['wait']) / 1e9)
        if abs(actual - report['rate']) > .001 or abs(throughput - report['throughput']) > .001:
            raise ValueError('Rate/throughput reconciliation failed')
        before = {s['container']: s['cpu_ns'] for s in run['before']}
        if set(before) != {s['container'] for s in run['after']}:
            raise ValueError('Container identity changed within run')
        cpu_ns = sum(s['cpu_ns'] - before[s['container']] for s in run['after'])
        snapshots = [run['before'], run['after']] + [s['stats'] for s in samples]
        if cpu_ns < 0 or any(set(before) != {s['container'] for s in snapshot} for snapshot in snapshots):
            raise ValueError('Incomplete resource samples')
        peak = max(sum(s['working_set_bytes'] for s in snapshot) for snapshot in snapshots)
        rows.append(dict(product=run['product'], repetition=run['repetition'], target_rps=run['offered_rps'],
                         issued_requests=count, actual_issued_rps=actual, successful_rps=throughput,
                         p95_ms=report['latencies']['95th'] / 1e6, cpu_ms_per_request=cpu_ns / 1e6 / count,
                         peak_working_set_mib=peak / 2**20, success=report['success'],
                         target_reached=actual >= run['offered_rps'] * .99))
    if seen != expected:
        raise ValueError('Expected 18 distinct runs')
    sizes = {p: set(metadata['products'][p]['response_bytes']) for p in PRODUCTS}
    if any(v != {4513} for v in sizes.values()):
        raise ValueError('Response size differs from the controlled 4513-byte fixture')
    aggregates = []
    for rate in (50, 200):
        for product in PRODUCTS:
            selected = [r for r in rows if r['product'] == product and r['target_rps'] == rate]
            entry = dict(product=product, target_rps=rate, repetitions=len(selected), all_targets_reached=all(r['target_reached'] for r in selected))
            for metric in ('p95_ms', 'actual_issued_rps', 'successful_rps', 'cpu_ms_per_request', 'peak_working_set_mib'):
                values = [r[metric] for r in selected]
                entry[metric] = dict(median=median(values), min=min(values), max=max(values))
            aggregates.append(entry)
    return dict(measured_commit=metadata['commit'], measured_utc=metadata['utc'], rows=rows, aggregates=aggregates,
                metadata_sha256=hashlib.sha256((source / 'metadata.json').read_bytes()).hexdigest())

def table(summary, rate):
    lines = ['| Deployment | Issued req/s | Successful req/s | p95 latency, ms | Peak working set, MiB |',
             '| --- | ---: | ---: | ---: | ---: |']
    def value(cell):
        return f"{cell['median']:.2f} ({cell['min']:.2f}–{cell['max']:.2f})"
    for row in summary['aggregates']:
        if row['target_rps'] == rate:
            lines.append('| ' + PRODUCTS[row['product']] + ' | ' + ' | '.join(value(row[k]) for k in ('actual_issued_rps','successful_rps','p95_ms','peak_working_set_mib')) + ' |')
    return '\n'.join(lines)

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    summary = analyze(args.source)
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    (args.output / 'tables.md').write_text('Median (min–max), three repetitions per deployment and target.\n\n## Target 50 requests/s\n\n' + table(summary,50) + '\n\n## Target 200 requests/s\n\n' + table(summary,200) + '\n')
    print('Validated 18 runs; summary and tables written. Edge is unavailable, not zero.')
