#!/usr/bin/env python3
"""Isolated Linux server + real rtty client. Requires root; never touches systemd."""
import argparse,base64,hashlib,http.cookiejar,json,os,pathlib,pwd,secrets,socket,ssl,subprocess,tempfile,time,urllib.error,urllib.request,uuid
p=argparse.ArgumentParser();p.add_argument('--server',required=True);p.add_argument('--client',required=True);p.add_argument('--os-user',required=True);p.add_argument('--binary-check');args=p.parse_args()
root=pathlib.Path(tempfile.mkdtemp(prefix='rttys-operations-'));root.chmod(0o755)
user=pwd.getpwnam(args.os_user);assert user.pw_uid!=0
files=root/'files';files.mkdir();os.chown(files,user.pw_uid,user.pw_gid)
(files/'escape').symlink_to('/etc/passwd')
# Force a known OS-command failure through the real command pipeline.
fake=root/'bin';fake.mkdir();(fake/'journalctl').write_text('#!/bin/sh\necho "fixture journal access denied" >&2\nexit 7\n');(fake/'journalctl').chmod(0o755)

password=secrets.token_urlsafe(24);(root/'password').write_text(password);(root/'password').chmod(0o600)
(root/'tls.cnf').write_text('[req]\ndistinguished_name=dn\nx509_extensions=ext\nprompt=no\n[dn]\nCN=127.0.0.1\n[ext]\nsubjectAltName=IP:127.0.0.1\nbasicConstraints=CA:TRUE\n')
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-config',str(root/'tls.cnf'),'-keyout',str(root/'key.pem'),'-out',str(root/'cert.pem')],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
ports=[]
for _ in range(3):
 with socket.socket() as s:s.bind(('127.0.0.1',0));ports.append(s.getsockname()[1])
base='https://127.0.0.1:'+str(ports[0]);ctx=ssl.create_default_context(cafile=str(root/'cert.pem'))
op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),urllib.request.HTTPSHandler(context=ctx))
# Runtime collection uses the same real command transport with a controlled Docker response.
runtime_state=root/'runtime-state';runtime_state.write_text('v1');runtime_state.chmod(0o644)
docker=fake/'docker'
docker.write_text('#!/usr/bin/env python3\nimport json,sys,os\nfrom pathlib import Path\ns=Path('+repr(str(runtime_state))+').read_text()\nif s=="fail":sys.exit(1)\nif s=="v2" and not os.environ.get("RTTY_TEST_SUDO"):\n print("permission denied",file=sys.stderr);sys.exit(1)\nprint(json.dumps([{ "State":{"Running":True},"Image":"sha256:"+"a"*64,"Config":{"Image":"psdk:"+s,"Labels":{"org.opencontainers.image.version":s}}}]))\n')
docker.chmod(0o755)
(fake/'sudo').write_text('#!/bin/sh\n[ "$1" = -n ] || exit 1\nshift\nRTTY_TEST_SUDO=1 exec "$@"\n')
(fake/'sudo').chmod(0o755)
checks=[];server=None;client=None
def check(name,actual,expected):
 checks.append(dict(name=name,actual=actual,expected=expected,passed=actual==expected));(root/'checks.json').write_text(json.dumps(checks,indent=2));print(name,actual==expected,flush=True);assert actual==expected,name
def req(path,data=None,method=None):
 try:r=op.open(urllib.request.Request(base+path,data=None if data is None else json.dumps(data).encode(),method=method,headers={'Origin':base,'Content-Type':'application/json'}),timeout=40)
 except urllib.error.HTTPError as e:r=e
 with r:
  raw=r.read();return r.code,json.loads(raw) if raw else None
try:
 server=subprocess.Popen([args.server,'--tenant-db',str(root/'identity.db'),'--bootstrap-password-file',str(root/'password'),'--addr-user','127.0.0.1:'+str(ports[0]),'--addr-dev','127.0.0.1:'+str(ports[1]),'--addr-http-proxy','127.0.0.1:'+str(ports[2]),'--sslcert',str(root/'cert.pem'),'--sslkey',str(root/'key.pem')],stdout=(root/'server.log').open('w'),stderr=subprocess.STDOUT)
 for _ in range(100):
  try:
   with socket.create_connection(('127.0.0.1',ports[0]),.2):break
  except OSError:time.sleep(.1)
 check('login',req('/api/signin',{'username':'admin','password':password})[0],200)
 code,tenant=req('/api/manage/tenants',{'name':'Operations E2E'});check('tenant created',code,201);req('/api/tenant',{'tenant_id':tenant['id']})
 code,device=req('/api/manage/devices',{'wire_id':'ops-real-client','name':'Operations client','web_targets':[]});check('device created',code,201)
 code,offline=req('/api/manage/devices',{'wire_id':'ops-offline','name':'Offline fixture','web_targets':[]});check('offline fixture created',code,201)
 endpoint='/api/devices/'+device['id']+'/files'
 def fileop(data):return req(endpoint,data)
 check('root execution forbidden',req('/api/manage/devices/'+device['id']+'/operations',{'username':'root','root':str(files)},'PUT')[0],400)
 check('configure non-root directory',req('/api/manage/devices/'+device['id']+'/operations',{'username':args.os_user,'root':str(files)},'PUT')[0],200)
 (root/'client.conf').write_text('id: ops-real-client\nhost: 127.0.0.1\nport: '+str(ports[1])+'\nusername: '+args.os_user+'\nssl: true\ncacert: '+str(root/'cert.pem')+'\nreconnect: true\nheartbeat: 5\ntoken: '+device['token']+'\n');(root/'client.conf').chmod(0o600)
 client=subprocess.Popen([args.client,'--conf',str(root/'client.conf')],env=dict(os.environ,PATH=str(fake)+':'+os.environ['PATH']),stdout=(root/'client.log').open('w'),stderr=subprocess.STDOUT)
 for _ in range(80):
  code,overview=req('/api/devices/'+device['id']+'/overview')
  if code==200 and overview['connection']['online']:break
  time.sleep(.2)
 check('real client online',overview['connection']['online'],True)
 for _ in range(100):
  _,overview=req('/api/devices/'+device['id']+'/overview')
  if overview['runtime']['status'] in ['ready','failed']:break
  time.sleep(.2)
 check('registration automatically collects runtime',overview['runtime']['status'],'ready')
 check('running container image',overview['runtime']['info']['image'],'psdk:v1')
 collected=overview['runtime']['collected_at']
 client.terminate();client.wait(timeout=10);time.sleep(.5)
 runtime_state.write_text('fail')
 client=subprocess.Popen([args.client,'--conf',str(root/'client.conf')],env=dict(os.environ,PATH=str(fake)+':'+os.environ['PATH']),stdout=(root/'client-reconnect.log').open('w'),stderr=subprocess.STDOUT)
 for _ in range(100):
  _,overview=req('/api/devices/'+device['id']+'/overview')
  if overview['runtime']['status']=='failed':break
  time.sleep(.2)
 check('reconnect triggers failed collection',overview['runtime']['status'],'failed')
 check('failed collection retains previous image',overview['runtime']['info']['image'],'psdk:v1')
 check('failed collection preserves success timestamp',overview['runtime']['collected_at'],collected)
 runtime_state.write_text('v2')
 client.terminate();client.wait(timeout=10);time.sleep(.5)
 client=subprocess.Popen([args.client,'--conf',str(root/'client.conf')],env=dict(os.environ,PATH=str(fake)+':'+os.environ['PATH']),stdout=(root/'client-recovery.log').open('w'),stderr=subprocess.STDOUT)
 for _ in range(100):
  _,overview=req('/api/devices/'+device['id']+'/overview')
  if overview['runtime']['status']=='ready' and overview['runtime']['info'].get('image')=='psdk:v2':break
  time.sleep(.2)
 check('sudo fallback refreshes image',overview['runtime']['info']['image'],'psdk:v2')
 check('device list exposes same image',next(d for d in req('/api/devs')[1] if d['id']==device['id'])['runtime']['info']['image'],'psdk:v2')

 check('list directory',fileop({'action':'list','path':'.'})[0],200)
 for path in ['../../etc/passwd','/etc/passwd','escape']:
  check('outside directory denied '+path,fileop({'action':'read','path':path})[0],409)
 payload=bytes(range(256))*257;transfer=str(uuid.uuid4());name='proof ; 中文.bin'
 check('begin upload',fileop({'action':'begin','path':name,'transfer':transfer,'size':len(payload)})[0],200)
 for offset in range(0,len(payload),16384):
  data={'action':'write','transfer':transfer,'offset':offset,'data':base64.b64encode(payload[offset:offset+16384]).decode()}
  check('upload block '+str(offset),fileop(data)[0],200)
  if offset==0:check('identical chunk retry',fileop(data)[0],200)
 digest=hashlib.sha256(payload).hexdigest();code,result=fileop({'action':'finish','transfer':transfer,'sha256':digest});check('commit verified upload',code,200);check('upload digest',result['sha256'],digest)
 check('never overwrite existing file',fileop({'action':'begin','path':name,'transfer':str(uuid.uuid4()),'size':1})[0],409)
 downloaded=b'';stamp=''
 while len(downloaded)<len(payload):
  code,result=fileop({'action':'read','path':name,'offset':len(downloaded),'stamp':stamp});check('download block '+str(len(downloaded)),code,200);stamp=result['stamp'];downloaded+=base64.b64decode(result['data'])
 check('download integrity',hashlib.sha256(downloaded).hexdigest(),digest)
 check('real device file integrity',hashlib.sha256((files/name).read_bytes()).hexdigest(),digest)
 transfer=str(uuid.uuid4());check('begin cancellable upload',fileop({'action':'begin','path':'cancelled.bin','size':100,'transfer':transfer})[0],200);check('cancel upload',fileop({'action':'cancel','transfer':transfer})[0],200);check('cancel leaves no destination',(files/'cancelled.bin').exists(),False);check('cancel cleans staging',len(list((files/'.rttys-transfers').iterdir())),0)
 task=str(uuid.uuid4());check('unsafe template denied',req('/api/tasks',{'id':task,'template':'shell','device_ids':[device['id']]})[0],400)
 check('batch accepted',req('/api/tasks',{'id':task,'template':'disk','device_ids':[device['id'],device['id'],offline['id']]})[0],202)
 for _ in range(80):
  _,jobs=req('/api/tasks');job=next(j for j in jobs if j['id']==task)
  if job['state']=='finished':break
  time.sleep(.5)
 check('batch finished',job['state'],'finished');check('targets deduplicated',len(job['targets']),2);check('online success offline failure',[r['status'] for r in job['results']],['success','failed'])
 (root/'task.json').write_text(json.dumps(job,indent=2))
 task=str(uuid.uuid4());check('failure template accepted',req('/api/tasks',{'id':task,'template':'logs','device_ids':[device['id']]})[0],202)
 for _ in range(80):
  _,jobs=req('/api/tasks');job=next(j for j in jobs if j['id']==task)
  if job['state']=='finished':break
  time.sleep(.5)
 check('nonzero OS exit is failed',job['results'][0]['status'],'failed')
 check('nonzero OS exit preserved',job['results'][0]['output']['exit_code'],7)

 check('configure filesystem root without root user',req('/api/manage/devices/'+device['id']+'/operations',{'username':args.os_user,'root':'/'},'PUT')[0],200)
 check('list filesystem root',fileop({'action':'list','path':'.'})[0],200)
 check('enter absolute owned directory',fileop({'action':'list','path':str(files)})[0],200)
 check('go to parent',fileop({'action':'list','path':str(files.parent)})[0],200)
 check('OS permissions still deny root home',fileop({'action':'list','path':'/root'})[0],409)
 transfer=str(uuid.uuid4());check('root scope upload staging',fileop({'action':'begin','path':str(files/'root-scope.bin'),'size':0,'transfer':transfer})[0],200);check('root scope cancel',fileop({'action':'cancel','transfer':transfer})[0],200)
 if args.binary_check:
  private=root/'binary-access.json';private.write_text(json.dumps({'url':base,'tenant_a':tenant,'device':device,'users':{'admin':{'password':password}}}));private.chmod(0o600)
  result=subprocess.run([args.binary_check,'-access',str(private),'-ca',str(root/'cert.pem'),'-source',str(files/name),'-directory',str(files),'-output',str(root/'binary-checks.json')],timeout=180)
  check('real binary file transport',result.returncode,0)
finally:
 for process in [client,server]:
  if process:
   process.terminate()
   try:process.wait(timeout=10)
   except subprocess.TimeoutExpired:process.kill();process.wait()
 print('EVIDENCE='+str(root),flush=True)
