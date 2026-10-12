#!/usr/bin/env python3
"""Build an offline service-name ruleset from pinned, attributed source snapshots.
No archive extraction, shell commands, live DNS queries, or runtime downloads.
"""
import argparse
import hashlib
import json
import ipaddress
import pathlib
import re
import tarfile
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
OUT = ROOT / 'apps/traffic-monitor/internal/monitor/rules'
DEFAULT_COMMIT = 'e84921279c9142434b43cb84ec6c17f987e025df'
# Routing/category lists and registry TLDs are not individual service identities.
EXCLUDED = {'cn', 'private', 'test', 'connectivity-check', 'google-registry', 'google-registry-tld', 'speedtest', 'fonts', 'books', 'code', 'anime', 'sci', 'snodehome'}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--commit', default=DEFAULT_COMMIT, help='Full reviewed upstream Git commit')
    args = parser.parse_args()
    if not re.fullmatch('[0-9a-f]{40}', args.commit):
        parser.error('commit must be 40 lowercase hex characters')
    archive = ROOT / f'cache/downloads/domain-list-community-{args.commit}.tar.gz'
    archive.parent.mkdir(parents=True, exist_ok=True)
    if not archive.exists():
        url = f'https://codeload.github.com/v2fly/domain-list-community/tar.gz/{args.commit}'
        with urllib.request.urlopen(url, timeout=60) as response:
            archive.write_bytes(response.read())
    with tarfile.open(archive) as tar:
        files = {m.name.rsplit('/', 1)[1]: tar.extractfile(m).read().decode('utf-8') for m in tar.getmembers() if '/data/' in m.name and m.isfile()}
        license_text = tar.extractfile(next(m for m in tar.getmembers() if m.name.endswith('/LICENSE'))).read()
    aliases = json.loads((OUT / 'service-names.json').read_text(encoding='utf-8'))
    candidates = {}
    includes = {}
    skipped = {'regex_or_keyword': 0, 'invalid': 0, 'ambiguous': 0}
    for service, content in sorted(files.items()):
        includes[service] = [x.split(':', 1)[1].split()[0] for line in content.splitlines() if (x := line.split('#', 1)[0].strip()).startswith('include:')]
        if service in EXCLUDED or service.startswith(('category-', 'geolocation-', 'tld-')):
            continue
        group = aliases.get(service, {}).get('group', service)
        for line in content.splitlines():
            line = line.split('#', 1)[0].strip()
            if not line or line.startswith('include:'):
                continue  # Source ownership is preserved instead of flattening parent companies.
            value = line.split()[0]
            kind = 'suffix'
            if ':' in value:
                kind, value = value.split(':', 1)
                if kind == 'domain':
                    kind = 'suffix'
                if kind not in ('suffix', 'full'):
                    skipped['regex_or_keyword'] += 1
                    continue
            value = value.lower().rstrip('.').encode('idna').decode('ascii')
            if len(value) > 253 or not all(re.fullmatch(r'[a-z0-9_](?:[a-z0-9_-]*[a-z0-9_])?', label) for label in value.split('.')):
                skipped['invalid'] += 1
                continue
            candidates.setdefault((kind, value), set()).add(group)
    def descendants(name, seen=None):
        seen = set() if seen is None else seen
        for child in includes.get(name, []):
            if child not in seen:
                seen.add(child)
                descendants(child, seen)
        return {aliases.get(x, {}).get('group', x) for x in seen}
    trees = {name: descendants(name) for name in files}
    suffix, exact = {}, {}
    for (kind, domain), owners in sorted(candidates.items()):
        if len(owners) == 1:
            owner = next(iter(owners))
        else:
            choices = [x for x in owners if all(x == y or x in trees.get(y, set()) for y in owners)]
            if len(choices) != 1:
                skipped['ambiguous'] += 1
                continue
            owner = choices[0]
        (exact if kind == 'full' else suffix)[domain] = owner
    local = json.loads((OUT / 'local-overrides.json').read_text(encoding='utf-8'))
    for group, entry in local.items():
        aliases[group] = {'name': entry['name'], 'domain': entry['domain']}
        for kind, dest in [('suffix', suffix), ('exact', exact)]:
            for domain in entry.get(kind, []):
                if not re.fullmatch(r'[a-z0-9_-]+(?:\.[a-z0-9_-]+)+', domain):
                    raise ValueError('Invalid local domain override')
                dest[domain] = group
                # More-specific legacy parent-company entries must not split app
                # telemetry/API calls back out of their service's website group.
                if kind == 'suffix':
                    for table in (suffix, exact):
                        for child in list(table):
                            if child.endswith('.' + domain):
                                table[child] = group
    policy = json.loads((OUT / 'app-policy.json').read_text(encoding='utf-8'))
    manifest = json.loads((OUT / 'sources/manifest.json').read_text(encoding='utf-8'))
    approved_count = 0
    ip_rules = []
    for source in manifest['sources']:
        blob = (OUT / 'sources' / source['file']).read_bytes()
        if hashlib.sha256(blob).hexdigest() != source['sha256']:
            raise ValueError('Rule snapshot checksum mismatch: ' + source['file'])
        if source['file'] == 'telegram-cidr.txt':
            ip_rules = [{'cidr': str(ipaddress.ip_network(line.strip())), 'service':'telegram', 'source':source['url']} for line in blob.decode().splitlines() if line.strip()]
            continue
        service = source['service']
        allowed = set(policy['approved'].get(service, []))
        for line in blob.decode().splitlines():
            fields = line.split(',')
            if len(fields) < 2 or fields[0] not in ('DOMAIN','DOMAIN-SUFFIX') or fields[1] not in allowed:
                continue
            (exact if fields[0]=='DOMAIN' else suffix)[fields[1]] = service
            approved_count += 1
    for domain in policy['exclude_shared']:
        exact.pop(domain,None); suffix.pop(domain,None)
    for table in (suffix,exact):
        for domain,service in table.items():
            table[domain] = policy['families'].get(service,service)
    used = sorted(set(suffix.values()) | set(exact.values()))
    services = {s: {'name': aliases.get(s, {}).get('name', s), 'domain': aliases.get(s, {}).get('domain', '')} for s in used}
    rules = {'source': 'https://github.com/v2fly/domain-list-community', 'commit': args.commit,
             'archive_sha256': hashlib.sha256(archive.read_bytes()).hexdigest(),
             'licenses': {'domain-list-community':'MIT', 'ios_rule_script':'GPL-2.0', 'telegram_cidr':'Official public network list; see source URL'},
             'service_count': len(services), 'rule_count': len(suffix) + len(exact), 'local_service_count': len(local), 'skipped': skipped,
             'app_rule_source':'https://github.com/blackmatrix7/ios_rule_script', 'app_rule_commit':manifest['commit'],
             'reviewed_app_rules':approved_count, 'ip_rule_count':len(ip_rules), 'ip_rules':ip_rules,
             'services': services, 'suffix': suffix, 'exact': exact}
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / 'domains.json').write_text(json.dumps(rules, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    (OUT / 'LICENSE.domain-list-community').write_bytes(license_text)
    catalog = []
    for key, meta in services.items():
        domains = sorted({d for table in (suffix,exact) for d,s in table.items() if s==key})
        catalog.append({'key':key,**meta,'domain_rule_count':len(domains),'examples':domains[:8]})
    (OUT / 'service-catalog.json').write_text(json.dumps(catalog,ensure_ascii=False,indent=2)+'\n',encoding='utf8')
    print(json.dumps({k: v for k, v in rules.items() if k not in ('services', 'suffix', 'exact','ip_rules')}, ensure_ascii=False))

if __name__ == '__main__':
    main()
