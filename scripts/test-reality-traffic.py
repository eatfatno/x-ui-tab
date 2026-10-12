#!/usr/bin/env python3
"""Test local monitored VLESS/TCP/REALITY inbound through a private real exit.
Only fixed public website homepages are requested. Credentials stay in runtime.
"""
import copy
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import time
import uuid
import lab

ROOT = lab.ROOT
TAG = 'monitor-reality-validation'
EMAIL = 'monitor-reality-check'
PORT = 15444
SOCKS = 11082


def obj(value):
    return json.loads(value) if isinstance(value, str) else value


def main():
    private = ROOT / 'runtime/monitor/private'
    folder = private / ('reality-' + str(time.time_ns()))
    folder.mkdir(parents=True)
    outbound = json.loads((private / 'user-reality-outbound.json').read_text())
    (private / 'user-reality-outbound.json').chmod(0o600)
    executable = lab.PANEL / 'bin/xray-linux-amd64'
    generated = subprocess.run([str(executable), 'x25519'], capture_output=True, text=True, check=True)
    keys = dict(line.split(': ',1) for line in generated.stdout.splitlines() if ': ' in line)
    public = keys.get('Password (PublicKey)') or keys.get('Password') or keys.get('PublicKey')
    assert public and keys.get('PrivateKey'), 'Xray key generation failed'
    original = obj(obj(lab.api('xray/', {}, 'POST'))['xraySetting'])
    lab.write_private(folder/'original-template.json', original)
    template = copy.deepcopy(original)
    template['outbounds'].append(outbound)
    template['routing']['rules'].insert(0, {'type':'field','inboundTag':[TAG],'outboundTag':outbound['tag']})
    # Reuse the same test identity on subsequent checks, without touching old tests.
    inbound = next((x for x in lab.api('inbounds/list') if x.get('tag')==TAG), None)
    client = next((x for x in lab.api('clients/list') if x['email']==EMAIL), None)
    identity = client.get('uuid') if client else None
    if inbound:
        identity = obj(inbound['settings'])['clients'][0]['id']
    identity = identity or str(uuid.uuid4())
    sid = os.urandom(8).hex()
    name = outbound['streamSettings']['realitySettings']['serverName']
    inbound_form = {'enable':'true','remark':'REALITY 实际网站访问验证','listen':'127.0.0.1','port':PORT,'protocol':'vless','tag':TAG,
        'settings':json.dumps({'clients':[{'id':identity,'email':EMAIL,'enable':True,'flow':'xtls-rprx-vision','totalGB':0,'expiryTime':0,'limitIp':0}],'decryption':'none'}),
        'streamSettings':json.dumps({'network':'tcp','security':'reality','realitySettings':{'show':False,'target':name+':443','dest':name+':443','xver':0,'serverNames':[name],'privateKey':keys['PrivateKey'],'shortIds':[sid]}}),
        'sniffing':json.dumps({'enabled':True,'destOverride':['http','tls','quic'],'routeOnly':False})}
    process = None
    report = {'started_at':datetime.now(timezone.utc).isoformat(),'protocol':'VLESS + TCP + REALITY + xtls-rprx-vision',
              'path':'local SOCKS -> monitored local REALITY inbound -> supplied remote REALITY exit -> website',
              'client_email':EMAIL,'requests':[],'passed':False}
    try:
        route='inbounds/update/'+str(inbound['id']) if inbound else 'inbounds/add'
        inbound=lab.api(route,inbound_form) or inbound
        if not inbound:
            inbound=next(x for x in lab.api('inbounds/list') if x.get('tag')==TAG)
        # v3.9.0 deliberately preserves enable on update; it has a separate API.
        lab.api('inbounds/setEnable/'+str(inbound['id']),{'enable':'true'})
        lab.api('xray/update',{'xraySetting':json.dumps(template)})
        lab.api('server/restartXrayService',{})
        for _ in range(50):
            try:
                with socket.create_connection(('127.0.0.1',PORT),timeout=.2): break
            except OSError: time.sleep(.2)
        else:
            raise RuntimeError('Local REALITY inbound did not start')
        local_out={'protocol':'vless','settings':{'vnext':[{'address':'127.0.0.1','port':PORT,'users':[{'id':identity,'encryption':'none','flow':'xtls-rprx-vision'}]}]},
            'streamSettings':{'network':'tcp','security':'reality','realitySettings':{'fingerprint':'chrome','serverName':name,'publicKey':public,'shortId':sid}}}
        config={'log':{'loglevel':'warning'},'inbounds':[{'listen':'127.0.0.1','port':SOCKS,'protocol':'socks','settings':{'auth':'noauth','udp':True}}],'outbounds':[local_out]}
        lab.write_private(folder/'client.json',config)
        with (folder/'client.log').open('wb') as output:
            process=subprocess.Popen([str(executable),'run','-config',str(folder/'client.json')],cwd=folder,env=lab.env(),stdout=output,stderr=output)
        for _ in range(50):
            if process.poll() is not None: raise RuntimeError('REALITY client exited; inspect private client log')
            try:
                with socket.create_connection(('127.0.0.1',SOCKS),timeout=.2): break
            except OSError: time.sleep(.2)
        print('Local monitored REALITY inbound and private exit ready; establishing baseline.',flush=True)
        time.sleep(12)
        sites=[('https://www.baidu.com/','--socks5-hostname',False),('https://github.com/','--socks5-hostname',False),('https://www.youtube.com/','--socks5-hostname',False),('https://www.baidu.com/','--socks5',False),('https://www.baidu.com/','--socks5',True)]
        for url, socks_mode, client_sniffing in sites:
            if client_sniffing:
                process.terminate()
                process.wait(timeout=10)
                config['inbounds'][0]['sniffing']={'enabled':True,'destOverride':['http','tls','quic'],'routeOnly':False}
                lab.write_private(folder/'client.json',config)
                with (folder/'client.log').open('ab') as output:
                    process=subprocess.Popen([str(executable),'run','-config',str(folder/'client.json')],cwd=folder,env=lab.env(),stdout=output,stderr=output)
                time.sleep(1)
            request_start=time.time_ns()//1000000
            result=subprocess.run(['curl','--silent','--show-error','--max-time','25','--connect-timeout','12','--retry','1','--retry-all-errors','--retry-delay','1','--retry-max-time','55','--noproxy','',socks_mode,f'127.0.0.1:{SOCKS}',
                '--max-filesize','1048576','-o',os.devnull,'-w','%{http_code} %{size_download}',url],capture_output=True,text=True)
            fields=result.stdout.split()
            record={'url':url,'target_mode':'domain' if socks_mode=='--socks5-hostname' else 'locally_resolved_IP_with_TLS_sniffing','curl_exit':result.returncode,'http_status':int(fields[0]) if fields and fields[0].isdigit() else 0,'download_bytes':int(fields[1]) if len(fields)>1 and fields[1].isdigit() else 0}
            record.update(client_sniffing=client_sniffing,request_start_ms=request_start,request_end_ms=time.time_ns()//1000000)
            report['requests'].append(record)
            print(json.dumps(record),flush=True)
        print('Waiting for panel persistence and monitor collection.',flush=True)
        time.sleep(25)
        live=next(x for x in lab.api('clients/list') if x['email']==EMAIL)
        report['panel_traffic']={k:(live.get('traffic') or {}).get(k,0) for k in ('up','down')}
        monitor_config=json.loads((lab.MONITOR/'config.json').read_text())
        with sqlite3.connect('file:'+monitor_config['db_path']+'?mode=ro',uri=True) as db:
            since=int(datetime.fromisoformat(report['started_at']).timestamp()*1000)
            report['recorded_targets']=[{'target':r[0],'count':r[1]} for r in db.execute('SELECT target,COUNT(*) FROM visits WHERE email=? AND time>=? GROUP BY target',(EMAIL,since))]
            for record in report['requests']:
                record['logged_targets']=[r[0] for r in db.execute('SELECT target FROM visits WHERE email=? AND time>=? AND time<=?',(EMAIL,record['request_start_ms'],record['request_end_ms']))]
        report['server_sniffing_recovers_IP_in_access_log']='www.baidu.com' in report['requests'][3]['logged_targets']
        report['client_sniffing_passes_domain']='www.baidu.com' in report['requests'][4]['logged_targets']
        report['passed']=all(x['curl_exit']==0 and x['http_status'] in (200,301,302) and x['logged_targets'] for x in report['requests']) and {x['target'] for x in report['recorded_targets']} >= {'www.baidu.com','github.com','www.youtube.com'} and report['client_sniffing_passes_domain']
    finally:
        if process:
            process.terminate()
            try: process.wait(timeout=10)
            except subprocess.TimeoutExpired: process.kill();process.wait()
        if inbound:
            lab.api('inbounds/setEnable/'+str(inbound['id']),{'enable':'false'})
        lab.api('xray/update',{'xraySetting':json.dumps(original)})
        lab.api('server/restartXrayService',{})
        report['temporary_routing_restored']=True
        report['test_inbound_disabled']=True
        (ROOT/'artifacts/reports/reality-traffic.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(report,ensure_ascii=False),flush=True)
    if not report['passed']: raise SystemExit(1)


if __name__=='__main__': main()
