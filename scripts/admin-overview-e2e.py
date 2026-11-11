#!/usr/bin/env python3
"""Disposable real HTTP server: global admin overview and tenant isolation."""
import ssl, http.cookiejar, json, os, pathlib, socket, subprocess, tempfile, time, urllib.request, urllib.error
root=pathlib.Path(tempfile.mkdtemp(prefix='admin-overview-'))
password='Local-overview-Password123!'
(root/'password').write_text(password);(root/'password').chmod(0o600)
ports=[]
for _ in range(3):
    with socket.socket() as s:
        s.bind(('127.0.0.1',0));ports.append(s.getsockname()[1])
base=f'https://127.0.0.1:{ports[0]}'
cmd=[os.environ['RTTY_BINARY'],'--tenant-db',str(root/'identity.db'),'--bootstrap-password-file',str(root/'password'),'--addr-user',f'127.0.0.1:{ports[0]}','--addr-dev',f'127.0.0.1:{ports[1]}','--addr-http-proxy',f'127.0.0.1:{ports[2]}']
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(root/'key.pem'),'-out',str(root/'cert.pem'),'-days','1','-subj','/CN=localhost'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
cmd+=['--sslcert',str(root/'cert.pem'),'--sslkey',str(root/'key.pem')]
server=subprocess.Popen(cmd,stdout=(root/'server.log').open('w'),stderr=subprocess.STDOUT)
checks=[]
def client():return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
def req(c,m,path,data=None):
    r=urllib.request.Request(base+path,data=None if data is None else json.dumps(data).encode(),method=m,headers={'Content-Type':'application/json','Origin':base})
    try:r=c.open(r,timeout=10)
    except urllib.error.HTTPError as e:r=e
    with r:
        b=r.read();return r.code,json.loads(b) if b else None
def check(name,actual,expected):
    checks.append(dict(name=name,actual=actual,expected=expected,passed=actual==expected))
    assert actual==expected,name
try:
    for _ in range(100):
        try:
            with socket.create_connection(('127.0.0.1',ports[0]),timeout=.1):break
        except OSError:time.sleep(.1)
    c=client();check('admin login',req(c,'POST','/api/signin',{'username':'admin','password':password})[0],200)
    ts=[];ds=[]
    for name in ['Alpha','Beta']:
        code,t=req(c,'POST','/api/manage/tenants',{'name':name});check('create '+name,code,201);ts.append(t)
        check('select '+name,req(c,'POST','/api/tenant',{'tenant_id':t['id']})[0],200)
        code,d=req(c,'POST','/api/manage/devices',{'wire_id':name,'name':name,'web_targets':[]});check('enroll '+name,code,201);ds.append(d)
        check('new device default operations '+name,req(c,'GET','/api/devices/'+d['id']+'/overview')[1]['operations'],{'username':'nvidia','root':'/'})
        if name=='Alpha':check('customize first device',req(c,'PUT','/api/manage/devices/'+d['id']+'/operations',{'username':'nvidia','root':'/srv/custom'})[0],200)
    code,u=req(c,'POST','/api/manage/users',{'username':'reader','password':password});check('create reader',code,201)
    check('assign reader',req(c,'PUT','/api/manage/members',{'tenant_id':ts[0]['id'],'user_id':u['id'],'role':'viewer'})[0],200)
    fresh=client();req(fresh,'POST','/api/signin',{'username':'admin','password':password})
    me=req(fresh,'GET','/api/me')[1]
    check('default admin overview',me['user']['tenant_id'],'')
    check('virtual admin workspace',me['tenants'][0]['name'],'admin · 所有设备')
    devices=req(fresh,'GET','/api/devs')[1]
    check('all tenant devices',sorted(d['id'] for d in devices),sorted(d['id'] for d in ds))
    check('tenant labels',sorted(d['tenant_name'] for d in devices),['Alpha','Beta'])
    req(fresh,'POST','/api/tenant',{'tenant_id':ts[0]['id']})
    check('scoped admin list',len(req(fresh,'GET','/api/devs')[1]),1)
    check('return overview',req(fresh,'POST','/api/tenant',{'tenant_id':''})[0],200)
    check('all devices restored',len(req(fresh,'GET','/api/devs')[1]),2)
    reader=client();req(reader,'POST','/api/signin',{'username':'reader','password':password})
    check('reader isolated', [d['id'] for d in req(reader,'GET','/api/devs')[1]],[ds[0]['id']])
    check('reader cannot enter overview',req(reader,'POST','/api/tenant',{'tenant_id':''})[0],403)
    check('reader cannot see Beta detail',req(reader,'GET','/api/dev/'+ds[1]['id'])[0],404)
    req(c,'POST','/api/tenant',{'tenant_id':ts[0]['id']})
    check('later enrollment preserves custom settings',req(c,'GET','/api/devices/'+ds[0]['id']+'/overview')[1]['operations'],{'username':'nvidia','root':'/srv/custom'})
    server.terminate();server.wait(timeout=10)
    server=subprocess.Popen(cmd,stdout=(root/'restart.log').open('w'),stderr=subprocess.STDOUT)
    for _ in range(100):
        try:
            with socket.create_connection(('127.0.0.1',ports[0]),timeout=.1):break
        except OSError:time.sleep(.1)
    check('restart preserves custom settings',req(c,'GET','/api/devices/'+ds[0]['id']+'/overview')[1]['operations'],{'username':'nvidia','root':'/srv/custom'})
finally:
    server.terminate();server.wait(timeout=10)
    (root/'result.json').write_text(json.dumps({'command':cmd,'checks':checks},indent=2));print(root)
