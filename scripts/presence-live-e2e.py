#!/usr/bin/env python3
"""Authorized device reconnect over SSH + real presence API evidence. Restarts rtty only."""
import argparse,http.cookiejar,json,os,pathlib,ssl,subprocess,time,urllib.request
p=argparse.ArgumentParser();p.add_argument('--fixture',required=True);p.add_argument('--ca',required=True);p.add_argument('--device-host',required=True);p.add_argument('--output',required=True);a=p.parse_args()
f=json.loads(pathlib.Path(a.fixture).read_text());base=f['url'];device=f['device']['id'];out=pathlib.Path(a.output)
c=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=a.ca)))
records=[]
def req(path,data=None):
    r=urllib.request.Request(base+path,data=None if data is None else json.dumps(data).encode(),headers={'Content-Type':'application/json','Origin':base})
    with c.open(r,timeout=15) as res:
        b=res.read();return json.loads(b) if b else None
def check(name,ok,actual):
    records.append({'check':name,'passed':ok,'actual':actual});out.write_text(json.dumps(records,indent=2));assert ok,name
def state():return req('/api/presence?device_id='+device)
def wait_online(expected):
    for _ in range(30):
        s=state()
        if s['summary']['online']==expected:return s
        time.sleep(1)
    raise RuntimeError('online state timeout')
def control(action):
    # The password is supplied privately via environment, never in argv or evidence.
    result=subprocess.run(['sshpass','-e','ssh','-F','/dev/null','-o','ConnectTimeout=8','-o','ControlMaster=auto','-o','ControlPersist=120','-o','ControlPath=/tmp/rtty173-presence-ssh','nvidia@'+a.device_host,"sudo -S -p '' systemctl "+action+' rtty-tenant-poc'],env=dict(os.environ,SSHPASS=os.environ['DEVICE_PASSWORD']),input=os.environ['DEVICE_PASSWORD']+'\n',capture_output=True,text=True,timeout=20)
    if result.returncode:raise RuntimeError('device service control failed: '+result.stderr)
req('/api/signin',{'username':'admin','password':f['users']['admin']['password']})
before=wait_online(1);check('device initially online',True,before)
try:
    control('stop');offline=wait_online(0)
    check('offline event recorded',offline['events'][0]['offline_at'] is not None and offline['events'][0]['reason']=='connection_closed',offline)
finally:
    control('start')
after=wait_online(1)
check('reconnect adds exactly one session',after['summary']['connections']==before['summary']['connections']+1,after)
check('latest session is online',after['events'][0]['offline_at'] is None and after['recent'][0]['id']==device,after['events'][0])
print(out)
