#!/usr/bin/env python3
"""Disposable real HTTP server: global admin overview and tenant isolation."""
import struct, ssl, http.cookiejar, json, os, pathlib, socket, subprocess, tempfile, time, urllib.request, urllib.error
root=pathlib.Path(tempfile.mkdtemp(prefix='presence-e2e-'))
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
        b=r.read();return r.code,json.loads(b) if b and b[:1] in (b'{',b'[') else None
def check(name,actual,expected):
    checks.append(dict(name=name,actual=actual,expected=expected,passed=actual==expected))
    assert actual==expected,name

def ready():
    for _ in range(100):
        if server.poll() is not None:raise RuntimeError('server exited')
        try:
            with socket.create_connection(('127.0.0.1',ports[0]),timeout=.1):return
        except OSError:time.sleep(.1)
    raise RuntimeError('server readiness timeout')
def register(device,token=None):
    conn=ssl._create_unverified_context().wrap_socket(socket.create_connection(('127.0.0.1',ports[1])),server_hostname='localhost')
    conn.settimeout(3)
    data=b'\x04'+(device['wire_id']+'\0presence-fixture\0'+(token or device['token'])+'\0').encode()
    conn.sendall(struct.pack('!BH',0,len(data))+data)
    def exact(n):
        result=b''
        while len(result)<n:
            part=conn.recv(n-len(result))
            if not part:raise RuntimeError('closed during registration')
            result+=part
        return result
    _,length=struct.unpack('!BH',exact(3));code=exact(length)[0]
    return conn,code
def presence(c,path='/api/presence?days=7'):
    code,b=req(c,'GET',path);check('presence endpoint',code,200);return b
try:
    ready();admin=client();req(admin,'POST','/api/signin',{'username':'admin','password':password})
    check('anonymous history rejected',req(client(),'GET','/api/presence')[0],401)
    # RED before implementation: the API must exist before generating device events.
    presence(admin)
    tenants=[];devices=[]
    for name in ['Alpha','Beta']:
        _,t=req(admin,'POST','/api/manage/tenants',{'name':name});tenants.append(t)
        req(admin,'POST','/api/tenant',{'tenant_id':t['id']})
        _,d=req(admin,'POST','/api/manage/devices',{'wire_id':name,'name':name,'web_targets':[]});devices.append(d)
    _,user=req(admin,'POST','/api/manage/users',{'username':'reader','password':password})
    req(admin,'PUT','/api/manage/members',{'tenant_id':tenants[0]['id'],'user_id':user['id'],'role':'viewer'})
    reader=client();req(reader,'POST','/api/signin',{'username':'reader','password':password})
    req(admin,'POST','/api/tenant',{'tenant_id':''})
    bad,code=register(devices[0],'0'*64);check('invalid token rejected',code!=0,True);bad.close()
    a,code=register(devices[0]);check('accepted registration',code,0)
    dup,code=register(devices[0]);check('duplicate rejected',code!=0,True);dup.close()
    time.sleep(1.1)
    p=presence(admin);check('one online',p['summary']['online'],1);check('rejected registrations not counted',p['summary']['connections'],1)
    a.close();time.sleep(.3)
    p=presence(admin);check('disconnect recorded',p['summary']['disconnections'],1);check('duration nonzero',p['summary']['online_seconds']>=1,True)
    check('closed event',p['events'][0]['reason'],'connection_closed')
    b,code=register(devices[1]);check('second device registered',code,0)
    # No heartbeats: the real server must detect its normal timeout.
    time.sleep(8)
    p=presence(admin);check('timeout reason',p['events'][0]['reason'],'heartbeat_timeout');b.close()
    check('reader sees only own device',[d['wire_id'] for d in presence(reader)['recent']],['Alpha'])
    check('foreign device history hidden',req(reader,'GET','/api/presence?device_id='+devices[1]['id'])[0],404)
    check('invalid days rejected',req(admin,'GET','/api/presence?days=-1')[0],400)
    a,code=register(devices[0]);check('reconnect accepted',code,0)
    check('newest device first',presence(admin)['recent'][0]['wire_id'],'Alpha')
    server.kill();server.wait(timeout=10);a.close()
    server=subprocess.Popen(cmd,stdout=(root/'restart.log').open('w'),stderr=subprocess.STDOUT);ready()
    p=presence(admin);check('history survives restart',p['summary']['connections'],3)
    check('restart interruption recorded',p['events'][0]['reason'],'server_restart')
    check('no phantom online after restart',p['summary']['online'],0)
    check('interruption count',p['summary']['uncertain'],1)
    cursor=p['events'][0]['id'];older=presence(admin,'/api/presence?before='+str(cursor))
    check('history cursor exclusive',all(e['id']<cursor for e in older['events']),True)
finally:
    if server.poll() is None:server.terminate();server.wait(timeout=10)
    (root/'result.json').write_text(json.dumps({'command':cmd,'checks':checks,'fixture':'synthetic devices over real TLS, real HTTPS API'},indent=2));print(root)
