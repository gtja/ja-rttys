#!/usr/bin/env python3
"""Run once on 204. Keep credentials outside the repository."""
import pathlib,ssl,http.cookiejar,urllib.request,json,secrets
root=pathlib.Path('/opt/rttys-tenant-poc');out=root/'test-access.json'
assert not out.exists(),'Test accounts already provisioned'
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(root/'cert.pem'))))
base='https://172.21.30.204:18446'
def req(method,path,data=None):
 r=urllib.request.Request(base+path,data=json.dumps(data).encode() if data is not None else None,method=method,headers={'Content-Type':'application/json','Origin':base})
 with client.open(r,timeout=15) as f:
  b=f.read();return json.loads(b) if b else None
password=(root/'bootstrap-password').read_text().strip();req('POST','/api/signin',{'username':'admin','password':password})
a=req('POST','/api/manage/tenants',{'name':'GTJA测试'});b=req('POST','/api/manage/tenants',{'name':'隔离验证'})
result={'url':base,'admin_password':password,'tenant_a':a,'tenant_b':b,'users':{}}
for name,tenant,role in [('tenant-admin',a,'admin'),('operator',a,'operator'),('outsider',b,'admin'),('viewer',a,'viewer')]:
 pw=secrets.token_urlsafe(18);u=req('POST','/api/manage/users',{'username':name,'password':pw});req('PUT','/api/manage/members',{'tenant_id':tenant['id'],'user_id':u['id'],'role':role});result['users'][name]={'id':u['id'],'password':pw}
req('POST','/api/tenant',{'tenant_id':a['id']})
d=req('POST','/api/manage/devices',{'wire_id':'nvidia-156','name':'NVIDIA 实机','group':'test','web_targets':['http://127.0.0.1:8080','http://127.0.0.1:18083']});result['device']=d
req('PUT','/api/manage/grants',{'user_id':result['users']['operator']['id'],'device_id':d['id'],'access':True})
out.write_text(json.dumps(result,indent=2));out.chmod(0o600)
(root/'client.conf').write_text('id: nvidia-156\nhost: 172.21.30.204\nport: 19512\nssl: true\ncacert: /opt/rtty-tenant-poc/cert.pem\nreconnect: true\nheartbeat: 5\nusername: nvidia\ntoken: '+d['token']+'\n');(root/'client.conf').chmod(0o600)
(root/'access.txt').write_text('URL: '+base+'\nadmin: '+password+'\n'+'\n'.join(k+': '+v['password'] for k,v in result['users'].items())+'\n');(root/'access.txt').chmod(0o600)
print('Provisioned two tenants, four tenant accounts, and one enrolled device. Secrets stored privately.')
