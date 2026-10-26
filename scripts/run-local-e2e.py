#!/usr/bin/env python3
"""Run a disposable real server and persist complete RED/GREEN evidence."""
import pathlib,subprocess,os,time,tempfile,json,sys,socket,ssl,urllib.request,http.cookiejar,datetime,sqlite3,contextlib
root=pathlib.Path(tempfile.mkdtemp(prefix='rttys-e2e-'))
pw='Local-test-only-Password!123';(root/'password').write_text(pw);(root/'password').chmod(0o600)
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(root/'key.pem'),'-out',str(root/'cert.pem'),'-days','1','-subj','/CN=localhost'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
reserved=[socket.socket() for _ in range(6)]
for s in reserved:s.bind(('127.0.0.1',0))
ports=[s.getsockname()[1] for s in reserved];addresses=['127.0.0.1:'+str(p) for p in ports]
for s in reserved:s.close()
env=dict(os.environ,RTTY_URL='https://'+addresses[1],RTTY_ADMIN_PASSWORD=pw,RTTY_EVIDENCE=str(root/'http.json'),RTTY_FIXTURE=str(root/'http.fixture.json'),RTTY_DEVICE_ADDR=addresses[0],RTTY_PROXY_ADDR=addresses[2],RTTY_SOCKET_EVIDENCE=str(root/'sockets.json'))
args=[os.environ.get('RTTY_BINARY','/tmp/rttys-tenant'),'--tenant-db',str(root/'test.db'),'--bootstrap-password-file',str(root/'password'),'--sslcert',str(root/'cert.pem'),'--sslkey',str(root/'key.pem'),'--addr-dev',addresses[0],'--addr-user',addresses[1],'--addr-http-proxy',addresses[2],'--http-proxy-redir-url','https://proxy.test:'+str(ports[2])]
initial=list(args)
if os.environ.get("RTTY_LEGACY_BINARY"):initial[0]=os.environ["RTTY_LEGACY_BINARY"]
server=subprocess.Popen(initial,stdout=open(root/'server.log','w'),stderr=subprocess.STDOUT)
result={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'artifact_root':str(root),'commands':[args],'fixture':'synthetic devices; real HTTPS/TLS/WebSocket transport'}
try:
 for attempt in range(150):
  if server.poll() is not None:raise RuntimeError('server exited before readiness')
  try:
   with socket.create_connection(('127.0.0.1',ports[1]),timeout=.2):break
  except OSError:
   if server.poll() is not None:raise RuntimeError('server exited before readiness')
   time.sleep(.2)
 else:raise RuntimeError('server readiness timeout')
 duplicate=list(args)
 for old,new in zip(addresses[:3],addresses[3:]):duplicate=[new if s==old else s for s in duplicate]
 with (root/'duplicate-instance.log').open('w') as f:
  dup=subprocess.Popen(duplicate,stdout=f,stderr=subprocess.STDOUT)
  try:status=dup.wait(timeout=9)
  except subprocess.TimeoutExpired:dup.terminate();dup.wait();status=0
 result['second_instance_rejected']=status!=0
 if status==0:raise RuntimeError('second writer was not rejected')
 with (root/'http-run.log').open('w') as f:r=subprocess.run(['python3','scripts/tenant-e2e.py'],env=env,stdout=f,stderr=subprocess.STDOUT)
 result['http_exit']=r.returncode
 if r.returncode==0 and os.environ.get('RTTY_LEGACY_BINARY'):
  server.terminate();server.wait(timeout=10)
  server=subprocess.Popen(args,stdout=open(root/'migration.log','w'),stderr=subprocess.STDOUT)
  for attempt in range(100):
   if server.poll() is not None:raise RuntimeError('migration server exited')
   try:
    with socket.create_connection(('127.0.0.1',ports[1]),timeout=.2):break
   except OSError:time.sleep(.2)
  result['migrated_from']=os.environ['RTTY_LEGACY_BINARY']
 if r.returncode==0:
  with (root/'socket-run.log').open('w') as f:r=subprocess.run([os.environ.get('RTTY_SOCKET_BINARY','/tmp/rttys-socket-check')],env=env,stdout=f,stderr=subprocess.STDOUT)
  result['socket_exit']=r.returncode
 if r.returncode==0:
  server.terminate();server.wait(timeout=10)
  server=subprocess.Popen(args,stdout=open(root/'restart.log','w'),stderr=subprocess.STDOUT)
  for attempt in range(100):
   try:
    with socket.create_connection(('127.0.0.1',ports[1]),timeout=.2):break
   except OSError:time.sleep(.2)
  fixture=json.loads((root/'http.fixture.json').read_text())
  jar=http.cookiejar.CookieJar();c=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
  request=urllib.request.Request(env['RTTY_URL']+'/api/signin',data=json.dumps({'username':fixture['users']['bob']['username'],'password':fixture['password']}).encode(),headers={'Content-Type':'application/json','Origin':env['RTTY_URL']})
  with c.open(request,timeout=5) as response:assert response.status==200
  with c.open(env['RTTY_URL']+'/api/devs',timeout=5) as response:devices=json.load(response)
  result['restart_persistence']=len(devices)==1 and devices[0]['id']==fixture['device_b']['id']
  if not result['restart_persistence']:raise RuntimeError('restart lost persisted identity')
  result['registration_metadata_persistence']=devices[0].get('client_description')=='fixture' and devices[0].get('last_registered_at',0)>0
  if not result['registration_metadata_persistence']:raise RuntimeError('restart lost registration metadata')
except Exception as e:
 result['error']=str(e)
 raise
finally:
 server.terminate();server.wait(timeout=10)
 if (root/'test.db').exists():
  with contextlib.closing(sqlite3.connect((root/'test.db').as_uri()+'?mode=ro',uri=True)) as db:
   result['identity_schema']=db.execute('PRAGMA user_version').fetchone()[0]
 result['finished_at']=datetime.datetime.now(datetime.timezone.utc).isoformat()
 (root/'run.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,indent=2))
 if os.environ.get('RTTY_RUN_SUMMARY'):
  summary=pathlib.Path(os.environ['RTTY_RUN_SUMMARY']);summary.parent.mkdir(parents=True,exist_ok=True)
  summary.write_text(json.dumps(result,indent=2))
sys.exit(r.returncode)
