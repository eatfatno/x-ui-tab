#!/usr/bin/env python3
"""Fetch reviewed app-rule source snapshots. Run with Windows Python if WSL has no raw GitHub route."""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'apps/traffic-monitor/internal/monitor/rules/sources'
COMMIT = '036c097eb26c6a52c4f04ebcb6633043cb942669'
SERVICES = {'YouTube':'youtube','Telegram':'telegram','Twitter':'twitter','OpenAI':'openai',
            'WeChat':'wechat','BiliBili':'bilibili','TikTok':'tiktok','DouYin':'douyin',
            'Instagram':'instagram','Spotify':'spotify','Netflix':'netflix',
            'AliPay':'alipay','Pinduoduo':'pinduoduo','XiaoHongShu':'xiaohongshu'}

def main():
    OUT.mkdir(parents=True,exist_ok=True)
    tasks = {name: f'https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/{COMMIT}/rule/Clash/{name}/{name}.list' for name in SERVICES}
    tasks['LICENSE.ios_rule_script'] = f'https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/{COMMIT}/LICENSE'
    tasks['telegram-cidr'] = 'https://core.telegram.org/resources/cidr.txt'
    def get(item):
        name,url=item
        with urllib.request.urlopen(urllib.request.Request(url,headers={'User-Agent':'traffic-monitor-rule-review'}),timeout=30) as r: content=r.read()
        filename=name+'.txt'
        (OUT/filename).write_bytes(content)
        return {'file':filename,'url':url,'sha256':hashlib.sha256(content).hexdigest(),'service':SERVICES.get(name,'telegram' if name=='telegram-cidr' else ''),'license':'GPL-2.0-only' if name in SERVICES else 'see source'}
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool: entries=list(pool.map(get,tasks.items()))
    (OUT/'manifest.json').write_text(json.dumps({'commit':COMMIT,'sources':entries},ensure_ascii=False,indent=2)+'\n',encoding='utf8')
    print(f'Saved {len(entries)} source snapshots under {OUT}')

if __name__=='__main__':main()
