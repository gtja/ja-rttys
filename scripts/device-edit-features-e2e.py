#!/usr/bin/env python3
"""Real HTTPS/TLS API acceptance for device editing and tenant feature authorization."""
import pathlib,subprocess,tempfile,os,socket,ssl,http.cookiejar,urllib.request,urllib.error,json,time,uuid,struct
root=pathlib.Path(tempfile.mkdtemp(prefix='rttys-edit-features-'));password=uuid.uuid4().hex;(root/'password').write_text(password);(root/'password').chmod(0o600)
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-keyout',str(root/'key'),'-out',str(root/'cert'),'-subj','/CN=localhost'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
ports=[]
for _ in range(3):
 with socket.socket() as s:s.bind(('127.0.0.1',0));ports.append(s.getsockname()[1])
base='https://127.0.0.1:'+str(ports[0]);cmd=[os.environ['RTTY_BINARY'],'--tenant-db',str(root/'identity.db'),'--bootstrap-password-file',str(root/'password'),'--sslcert',str(root/'cert'),'--sslkey',str(root/'key'),'--addr-user','127.0.0.1:'+str(ports[0]),'--addr-dev','127.0.0.1:'+str(ports[1]),'--addr-http-proxy','127.0.0.1:'+str(ports[2]),'--http-proxy-redir-url','https://proxy.test'];server=None;peers=[];checks=[];report={'checks':checks,'passed':False,'command':cmd}
def client():return urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
def req(c,path,data=None,method=None):
 try:r=c.open(urllib.request.Request(base+path,data=None if data is None else json.dumps(data).encode(),method=method,headers={'Origin':base,'Content-Type':'application/json'}),timeout=8)
 except urllib.error.HTTPError as e:r=e
 with r:
  b=r.read()
  try:body=json.loads(b) if b else None
  except ValueError:body={'error':b.decode(errors='replace')}
  if r.code>=400:report.setdefault('http_failures',[]).append({'path':path,'status':r.code,'response':body})
  return r.code,body

def check(name,actual,expected):
 checks.append({'name':name,'actual':actual,'expected':expected,'passed':actual==expected});assert actual==expected,name

def start():
 global server
 server=subprocess.Popen(cmd,stdout=(root/'server.log').open('a'),stderr=subprocess.STDOUT)
 for _ in range(100):
  if server.poll() is not None:raise RuntimeError('server exited')
  try:
   with socket.create_connection(('127.0.0.1',ports[0]),.1):return
  except OSError:time.sleep(.1)
 raise RuntimeError('server startup timed out')

def register(wire,token):
 s=ssl._create_unverified_context().wrap_socket(socket.create_connection(('127.0.0.1',ports[1]),3),server_hostname='localhost');s.settimeout(3);b=b'\x04'+(wire+'\0proof\0'+token+'\0').encode();s.sendall(struct.pack('!BH',0,len(b))+b)
 def exact(n):
  b=b''
  while len(b)<n:
   part=s.recv(n-len(b));assert part;b+=part
  return b
 _,n=struct.unpack('!BH',exact(3));code=exact(n)[0];peers.append(s);return code
try:
 start();admin=client();check('platform login',req(admin,'/api/signin',{'username':'admin','password':password})[0],200)
 _,a=req(admin,'/api/manage/tenants',{'name':'Tenant A'});_,b=req(admin,'/api/manage/tenants',{'name':'Tenant B'});req(admin,'/api/tenant',{'tenant_id':a['id']})
 code,u=req(admin,'/api/manage/users',{'username':'tenant-owner','password':password});check('create tenant owner',code,201);req(admin,'/api/manage/members',{'tenant_id':a['id'],'user_id':u['id'],'role':'admin'},'PUT');owner=client();req(owner,'/api/signin',{'username':'tenant-owner','password':password})
 _,d=req(owner,'/api/manage/devices',{'wire_id':'platform-number','name':'Before','web_targets':[]});req(owner,'/api/manage/groups',{'name':'Project'});req(owner,'/api/manage/groups',{'name':'Operations'})
 edit='/api/manage/devices/'+d['id']+'/details'
 check('edit name and two groups',req(owner,edit,{'name':'After 编辑','groups':['Project','Operations']},'PUT')[0],200)
 _,device=req(owner,'/api/dev/'+d['id']);check('edited name readback',device['name'],'After 编辑');check('multi groups readback',device['groups'],['Operations','Project']);check('stable enrollment number',device['wire_id'],'platform-number')
 check('old token still registers after edit',register('changing-reported-id',d['token']),0)
 check('invalid group rejects whole edit',req(owner,edit,{'name':'Wrong partial name','groups':['Missing']},'PUT')[0],404);check('failed edit preserved name',req(owner,'/api/dev/'+d['id'])[1]['name'],'After 编辑')
 check('empty name rejected',req(owner,edit,{'name':'','groups':[]},'PUT')[0],400);check('missing groups rejected',req(owner,edit,{'name':'Wrong'},'PUT')[0],400)
 req(admin,'/api/tenant',{'tenant_id':b['id']});check('cross tenant edit hidden',req(admin,edit,{'name':'Cross','groups':[]},'PUT')[0],404);req(admin,'/api/tenant',{'tenant_id':a['id']})
 features='/api/manage/tenants/'+a['id']+'/features';on={'ota':True,'tasks':True,'files':True,'web':True};off={key:False for key in on}
 check('tenant cannot self-authorize',req(owner,features,{'features':off},'PUT')[0],403)
 check('platform disables tenant functions',req(admin,features,{'features':off},'PUT')[0],200);check('features exposed in current account',req(owner,'/api/me')[1]['features'],off)
 for path,data in [('/api/ota',None),('/api/tasks',None),('/api/devices/'+d['id']+'/files',{'action':'list','path':'.'}),('/api/devices/'+d['id']+'/file-transfer?direction=download&path=/tmp/proof',None),('/api/web/'+d['id']+'/http/127.0.0.1:8080/',None)]:check('disabled capability denied '+path,req(owner,path,data)[0],403)
 req(admin,'/api/tenant',{'tenant_id':b['id']});check('other tenant keeps own features',req(admin,'/api/me')[1]['features'],on);req(admin,'/api/tenant',{'tenant_id':a['id']})
 check('missing feature fields rejected',req(admin,features,{'features':{'ota':True}},'PUT')[0],400);check('unknown feature rejected',req(admin,features,{'features':dict(on,extra=True)},'PUT')[0],400)
 for s in peers:s.close()
 peers=[];server.terminate();server.wait(timeout=10);start();check('tenant features persist restart',req(owner,'/api/me')[1]['features'],off);check('device edit persists restart',req(owner,'/api/dev/'+d['id'])[1]['name'],'After 编辑')
 check('restore tenant functions',req(admin,features,{'features':on},'PUT')[0],200);check('tasks permitted after grant',req(owner,'/api/tasks')[0],200);check('OTA backend readiness separate from permission',req(owner,'/api/ota')[1],{'enabled':False})
 report['passed']=True
finally:
 for s in peers:s.close()
 if server:server.terminate();server.wait(timeout=10)
 (root/'result.json').write_text(json.dumps(report,ensure_ascii=False,indent=2));print(json.dumps({'root':str(root),'passed':report['passed'],'checks':len(checks)},ensure_ascii=False))
