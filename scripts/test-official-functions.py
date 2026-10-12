#!/usr/bin/env python3
"""Read-only browser coverage plus one temporary loopback QR fixture.

Creates and removes only the newly allocated inbound/client. Never resets or
changes existing client settings, counters or monitor history.
"""
import json
import os
import socket
import subprocess
import time
import uuid
import lab


def main():
    tag = 'panel-ui-fixture-'+str(time.time_ns())
    email = tag
    original_clients = {c['email']:c for c in lab.api('clients/list')}
    original_inbounds = {x['id']:x for x in lab.api('inbounds/list')}
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
    inbound = None
    report = {'passed':False,'temporary_loopback_fixture':True}
    try:
        inbound = lab.api('inbounds/add',{'enable':'true','remark':tag,'tag':tag,'listen':'127.0.0.1','port':port,'protocol':'vless',
            'settings':json.dumps({'clients':[{'id':str(uuid.uuid4()),'email':email,'subId':uuid.uuid4().hex[:16],
                'enable':True,'flow':'','totalGB':0,'expiryTime':0,'limitIp':0}],'decryption':'none'}),
            'streamSettings':json.dumps({'network':'tcp','security':'none'}),'sniffing':json.dumps({'enabled':False})})
        if not inbound:
            inbound=next(x for x in lab.api('inbounds/list') if x['tag']==tag)
        client=next(c for c in lab.api('clients/list') if c['email']==email)
        assert client.get('subId')
        links=lab.api('clients/subLinks/'+client['subId'])
        assert links and all(x.startswith('vless://') for x in links)
        subprocess.run(['node',str(lab.ROOT/'scripts/browser-tests/official-panel.mjs')],cwd=lab.ROOT,
            env={**os.environ,'QR_CLIENT':email},timeout=480,check=True)
        report['passed']=True
    finally:
        if inbound:
            current=next(x for x in lab.api('inbounds/list') if x['id']==inbound['id'])
            assert current['tag']==tag and current['listen']=='127.0.0.1' and current['port']==port
            lab.api('inbounds/del/'+str(inbound['id']),{})
        remaining=lab.api('clients/list')
        # 3.9 keeps detached clients when deleting an inbound; remove only ours.
        fixture=next((c for c in remaining if c['email']==email),None)
        if fixture:
            lab.api('clients/del/'+fixture['email'],{})
        after={c['email']:c for c in lab.api('clients/list')}
        assert set(after)==set(original_clients)
        for email,before in original_clients.items():
            assert after[email]==before,'Existing client modified'
        assert {x['id']:x for x in lab.api('inbounds/list')}==original_inbounds,'Existing inbound modified'
        report['original_clients_and_inbounds_preserved']=True
        report['temporary_fixture_removed']=True
        (lab.ROOT/'artifacts/reports/official-functions.json').write_text(json.dumps(report,indent=2)+'\n')


if __name__=='__main__':main()
