#!/usr/bin/env python3
"""Real HTTPS/TLS registration across config-only auth-mode switches."""
import pathlib,subprocess,os,time,tempfile,json,socket,ssl,urllib.request,urllib.error,http.cookiejar,struct
root=pathlib.Path(tempfile.mkdtemp(prefix='rttys-auth-mode-'));password='Auth-mode-fixture-123!'
(root/'password').write_text(password);(root/'password').chmod(0o600)
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(root/'key'),'-out',str(root/'cert'),'-days','1','-subj','/CN=localhost'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
ports=[]
for _ in range(3):
 with socket.socket() as s:s.bind(('127.0.0.1',0));ports.append(s.getsockname()[1])
base='https://127.0.0.1:'+str(ports[0]);checks=[];server=None;plaintext=False
def check(name,ok):
 checks.append({'name':name,'passed':bool(ok)});(root/'checks.json').write_text(json.dumps(checks,indent=2));print(name,ok,flush=True);assert ok,name
def client():return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
def req(c,method,path,data=None):
 try:r=c.open(urllib.request.Request(base+path,data=None if data is None else json.dumps(data).encode(),method=method,headers={'Origin':base,'Content-Type':'application/json'}),timeout=10)
 except urllib.error.HTTPError as e:r=e
 with r:
  b=r.read();return r.code,json.loads(b) if b else None
def start(mode='token',tenant='',auto=False,plain=False):
 global server,plaintext
 plaintext=plain
 if server:server.terminate();server.wait(timeout=10)
 conf=root/'server.conf';conf.write_text('device-auth-mode: '+mode+'\ndevice-compat-tenant: '+tenant+'\ndevice-compat-auto-enroll: '+str(auto).lower()+'\ndevice-compat-plaintext: '+str(plain).lower()+'\n')
 server=subprocess.Popen([os.environ['RTTY_BINARY'],'--conf',str(conf),'--tenant-db',str(root/'identity.db'),'--bootstrap-password-file',str(root/'password'),'--sslcert',str(root/'cert'),'--sslkey',str(root/'key'),'--addr-user','127.0.0.1:'+str(ports[0]),'--addr-dev','127.0.0.1:'+str(ports[1]),'--addr-http-proxy','127.0.0.1:'+str(ports[2])],stdout=(root/('server-'+mode+'.log')).open('a'),stderr=subprocess.STDOUT)
 for _ in range(100):
  if server.poll() is not None:raise RuntimeError('server failed to start')
  try:
   with socket.create_connection(('127.0.0.1',ports[0]),.1):return
  except OSError:time.sleep(.1)
 raise RuntimeError('startup timeout')
def register(wire,token):
 c=socket.create_connection(('127.0.0.1',ports[1]));c=c if plaintext else ssl._create_unverified_context().wrap_socket(c,server_hostname='localhost');c.settimeout(3)
 data=b'\x04'+(wire+'\0legacy\0'+token+'\0').encode();c.sendall(struct.pack('!BH',0,len(data))+data)
 def exact(n):
  b=b''
  while len(b)<n:
   chunk=c.recv(n-len(b));assert chunk;b+=chunk
  return b
 _,n=struct.unpack('!BH',exact(3));code=exact(n)[0];c.close();time.sleep(.1);return code
try:
 start();admin=client();check('admin login',req(admin,'POST','/api/signin',{'username':'admin','password':password})[0]==200)
 _,tenant=req(admin,'POST','/api/manage/tenants',{'name':'Compatibility'});req(admin,'POST','/api/tenant',{'tenant_id':tenant['id']});_,d=req(admin,'POST','/api/manage/devices',{'wire_id':'known','name':'Known','web_targets':[]})
 check('token mode rejects missing',register('known','')!=0);check('token mode rejects invalid',register('known','wrong')!=0)
 start('compat',tenant['id'])
 check('compat accepts missing',register('known','')==0);check('compat accepts invalid',register('known','wrong')==0)
 check('unknown ID denied without auto-enroll',register('unknown','')!=0)
 check('platform login still required',req(client(),'GET','/api/devs')[0]==401)
 req(admin,'PATCH','/api/manage/devices/'+d['id'],{'disabled':True});check('disabled device denied in compat',register('known','')!=0);req(admin,'PATCH','/api/manage/devices/'+d['id'],{'disabled':False})
 start('compat',tenant['id'],True);check('legacy unknown auto-enrolled explicitly',register('old-box-5g','')==0)
 _,rows=req(admin,'GET','/api/manage/snapshot');created=next(x for x in rows['devices'] if x['wire_id']=='old-box-5g');_,detail=req(admin,'GET','/api/dev/'+created['id']);check('auto enrollment bound to configured tenant',detail['tenant_id']==tenant['id'])
 _,overview=req(admin,'GET','/api/devices/'+created['id']+'/overview');check('auto enrollment default operations',overview['operations']=={'username':'nvidia','root':'/'})
 req(admin,'DELETE','/api/manage/devices/'+created['id']);check('tombstone not resurrected',register('old-box-5g','')!=0)
 start('compat',tenant['id'],False,True);check('old plaintext client accepted without token',register('known','')==0);check('old plaintext client accepted with invalid token',register('known','bad')==0)
 start('token');check('switch back rejects missing',register('known','')!=0);check('switch back rejects invalid',register('known','wrong')!=0);check('original valid token preserved',register('known',d['token'])==0)
finally:
 if server:server.terminate();server.wait(timeout=10)
 print('EVIDENCE='+str(root))
