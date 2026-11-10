#!/usr/bin/env python3
"""Create a real version via RTTYS; never deploy it to a device.
Run on204 with access to its private fixture, CA and staged model.
"""
import argparse,pathlib,json,ssl,urllib.request,urllib.error,http.cookiejar,uuid,hashlib
p=argparse.ArgumentParser();p.add_argument('--version',required=True);p.add_argument('--model',required=True);p.add_argument('--output',required=True);a=p.parse_args()
root=pathlib.Path('/opt/rttys-tenant-poc');f=json.loads((root/'test-access-psdk167.json').read_text());base='https://172.21.30.204:18446';ctx=ssl.create_default_context(cafile=str(root/'cert.pem'));c=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),urllib.request.HTTPSHandler(context=ctx))
def request(path,data=None,content='application/json'):
 if data is not None and not isinstance(data,bytes):data=json.dumps(data).encode()
 try:r=c.open(urllib.request.Request(base+path,data=data,headers={'Origin':base,'Content-Type':content}),timeout=110)
 except urllib.error.HTTPError as e:r=e
 with r:
  b=r.read();return r.status,json.loads(b) if b else None
report={}
try:
 assert request('/api/signin',{'username':'tenant-admin','password':f['users']['tenant-admin']['password']})[0]==200
 code,overview=request('/api/ota');assert code==200 and overview['can_create_version']
 model=pathlib.Path(a.model).read_bytes();boundary=uuid.uuid4().hex;parts=[]
 for name,value in [('version',a.version),('image','sha256:8a0bdcd63b4baa64fb5789c21aee9a8984dd3ea19485628eb055b624678aef9b'),('binary_sha256','0bdc7e59c9f822720d9fdd23fd43737ca9cc54357e1484023b1e9438737fbe31')]:parts.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="'+name+'"\r\n\r\n'+value+'\r\n').encode())
 parts.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="model"; filename="uavModel"\r\nContent-Type: application/octet-stream\r\n\r\n').encode()+model+b'\r\n');parts.append(('--'+boundary+'--\r\n').encode());body=b''.join(parts);ctype='multipart/form-data; boundary='+boundary
 code,result=request('/api/ota/versions',body,ctype);report.update(status=code,result=result);assert code==201,(code,result)
 assert result['state']=='ready' and result['model_sha256']==hashlib.sha256(model).hexdigest()
 code,overview=request('/api/ota');assert any(x['id']==result['artifact_id'] for x in overview['artifacts']);assert any(x['version']==a.version and x['state']=='ready' for x in overview['versions'])
 code,duplicate=request('/api/ota/versions',body,ctype);assert code==409
 report.update(listed_in_rttys=True,duplicate_rejected=True,device_deployment_created=False,passed=True)
finally:
 pathlib.Path(a.output).write_text(json.dumps(report,indent=2));print(json.dumps(report,indent=2))
