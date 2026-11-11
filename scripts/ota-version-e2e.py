#!/usr/bin/env python3
"""Real RTTYS+artifact tool; isolated HTTPS Mender fixture verifies signed bytes.
Does not prove deployment to a device or the real Mender storage service.
"""
import argparse,email,email.policy,hashlib,http.cookiejar,http.server,json,os,pathlib,socket,ssl,subprocess,tempfile,threading,time,urllib.error,urllib.request,uuid
p=argparse.ArgumentParser();p.add_argument('--server',required=True);p.add_argument('--tool',required=True);a=p.parse_args()
root=pathlib.Path(tempfile.mkdtemp(prefix='ota-version-e2e-'));root.chmod(0o700);records=[];server=None;accepted=[]
def check(name,actual,expected):
 records.append(dict(name=name,actual=actual,expected=expected,passed=actual==expected));(root/'checks.json').write_text(json.dumps(records,indent=2));print(name,actual==expected,flush=True);assert actual==expected,name
def free():
 with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=localhost','-addext','subjectAltName=IP:127.0.0.1','-keyout',str(root/'tls.key'),'-out',str(root/'ca.pem')],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
subprocess.run(['openssl','genrsa','-out',str(root/'sign.key'),'3072'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL);(root/'sign.key').chmod(0o600)
subprocess.run(['openssl','pkey','-in',str(root/'sign.key'),'-pubout','-out',str(root/'verify.pem')],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
class Backend(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a):pass
 def do_POST(self):
  size=int(self.headers.get('Content-Length','0'));data=self.rfile.read(size)
  if self.path.endswith('/auth/login'):
   self.send_response(200);self.end_headers();self.wfile.write(b'fixture-jwt');return
  if self.headers.get('Authorization')!='Bearer fixture-jwt':self.send_error(401);return
  msg=email.message_from_bytes(('Content-Type: '+self.headers['Content-Type']+'\r\nMIME-Version: 1.0\r\n\r\n').encode()+data,policy=email.policy.default)
  artifact=next(part.get_payload(decode=True) for part in msg.iter_parts() if part.get_param('name',header='content-disposition')=='artifact')
  path=root/('accepted-'+str(len(accepted))+'.mender');path.write_bytes(artifact)
  r=subprocess.run([a.tool,'read','-k',str(root/'verify.pem'),str(path)],capture_output=True,text=True);(root/'artifact-read.log').write_text(r.stdout+r.stderr)
  if r.returncode:self.send_error(400);return
  identifier=str(uuid.uuid4());name=PREFIX+'psdk-release1';accepted.append({'id':identifier,'name':name,'device_types_compatible':['ja-nvidia-arm64-app']})
  self.send_response(201);self.send_header('Location','/api/management/v1/deployments/artifacts/'+identifier);self.end_headers()
 def do_GET(self):
  self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(json.dumps(accepted).encode())
backend=http.server.ThreadingHTTPServer(('127.0.0.1',0),Backend);tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(str(root/'ca.pem'),str(root/'tls.key'));backend.socket=tls.wrap_socket(backend.socket,server_side=True);threading.Thread(target=backend.serve_forever,daemon=True).start()
base='https://127.0.0.1:'+str(free());port=base.rsplit(':',1)[1];password='Version-fixture-Password!';(root/'password').write_text(password);(root/'password').chmod(0o600)
def client():return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(root/'ca.pem'))))
def request(c,path,data=None,content='application/json',method=None):
 if data is not None and not isinstance(data,bytes):data=json.dumps(data).encode()
 try:r=c.open(urllib.request.Request(base+path,data=data,method=method,headers={'Origin':base,'Content-Type':content}),timeout=100)
 except urllib.error.HTTPError as e:r=e
 with r:
  b=r.read();return r.code,json.loads(b) if b else None
def launch():
 global server
 server=subprocess.Popen([a.server,'--tenant-db',str(root/'identity.db'),'--bootstrap-password-file',str(root/'password'),'--addr-user','127.0.0.1:'+port,'--addr-dev','127.0.0.1:'+str(free()),'--addr-http-proxy','127.0.0.1:'+str(free()),'--sslcert',str(root/'ca.pem'),'--sslkey',str(root/'tls.key')]+(['--mender-config',str(root/'mender.json')] if (root/'mender.json').exists() else []),stdout=(root/'server.log').open('a'),stderr=subprocess.STDOUT)
 for _ in range(100):
  if server.poll() is not None:raise RuntimeError('server exited')
  try:
   with socket.create_connection(('127.0.0.1',int(port)),.1):return
  except OSError:time.sleep(.1)
def multipart(version='release1',image='sha256:'+'a'*64,model=b'{"model":"valid"}'):
 boundary=uuid.uuid4().hex;parts=[]
 for name,value in [('version',version),('image',image),('binary_sha256','b'*64)]:parts.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="'+name+'"\r\n\r\n'+value+'\r\n').encode())
 parts.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="model"; filename="uavModel"\r\nContent-Type: application/octet-stream\r\n\r\n').encode()+model+b'\r\n');parts.append(('--'+boundary+'--\r\n').encode());return b''.join(parts),'multipart/form-data; boundary='+boundary
try:
 launch();admin=client();check('admin login',request(admin,'/api/signin',{'username':'admin','password':password})[0],200)
 _,tenant=request(admin,'/api/manage/tenants',{'name':'Version tenant'});request(admin,'/api/tenant',{'tenant_id':tenant['id']});PREFIX='rttys-'+tenant['id'][:12]+'-'
 _,operator=request(admin,'/api/manage/users',{'username':'operator','password':password});request(admin,'/api/manage/members',{'tenant_id':tenant['id'],'user_id':operator['id'],'role':'operator'},method='PUT')
 server.terminate();server.wait(timeout=10)
 (root/'mender.json').write_text(json.dumps({'url':'https://127.0.0.1:'+str(backend.server_port),'ca_file':str(root/'ca.pem'),'username':'fixture','password':'fixture','artifact_prefixes':{tenant['id']:PREFIX},'bindings':{},'artifact_tool':a.tool,'signing_keys':{tenant['id']:str(root/'sign.key')}}));launch()
 request(admin,'/api/signin',{'username':'admin','password':password});request(admin,'/api/tenant',{'tenant_id':tenant['id']})
 body,content=multipart();check('anonymous creation denied',request(client(),'/api/ota/versions',body,content)[0],401)
 op=client();request(op,'/api/signin',{'username':'operator','password':password});check('operator creation denied',request(op,'/api/ota/versions',body,content)[0],403)
 bad,ct=multipart(image='shell;bad');check('invalid image denied',request(admin,'/api/ota/versions',bad,ct)[0],400)
 bad,ct=multipart(model=b'');check('empty model denied',request(admin,'/api/ota/versions',bad,ct)[0],400)
 code,result=request(admin,'/api/ota/versions',body,content);check('build sign and import',code,201);check('ready state',result['state'],'ready');check('tenant prefix generated',result['artifact'],PREFIX+'psdk-release1');check('model digest',result['model_sha256'],hashlib.sha256(b'{"model":"valid"}').hexdigest())
 check('duplicate version denied',request(admin,'/api/ota/versions',body,content)[0],409);check('only one upstream artifact',len(accepted),1)
 _,overview=request(admin,'/api/ota');check('version record ready',overview['versions'][0]['state'],'ready');check('artifact selectable',overview['artifacts'][0]['id'],result['artifact_id'])
 check('private signing settings never exposed','signing_keys' in json.dumps(overview) or str(root/'sign.key') in json.dumps(overview),False)
 (root/'sign.key').write_text('invalid signing key')
 bad,ct=multipart(version='signer-failure');check('signing failure reported',request(admin,'/api/ota/versions',bad,ct)[0],502)
 check('failed signing never uploads artifact',len(accepted),1)
 _,overview=request(admin,'/api/ota');check('failed version retained',next(v['state'] for v in overview['versions'] if v['version']=='signer-failure'),'failed')

finally:
 if server:server.terminate();server.wait(timeout=10)
 backend.shutdown();print('EVIDENCE='+str(root),flush=True)
