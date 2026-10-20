#!/usr/bin/env python3
"""Black-box HTTP contract; run before/after implementation against a live server.
Requires RTTY_URL, RTTY_ADMIN_PASSWORD; writes an evidence JSON even on failure.
"""
import os,json,ssl,urllib.request,urllib.error,http.cookiejar,sys,time,pathlib
base=os.environ.get('RTTY_URL','http://127.0.0.1:19513')
out=pathlib.Path(os.environ.get('RTTY_EVIDENCE','/tmp/rttys-e2e.json'))
records=[]
def client():
 jar=http.cookiejar.CookieJar()
 return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
def req(c,m,path,body=None):
 r=urllib.request.Request(base+path,data=json.dumps(body).encode() if body is not None else None,method=m,headers={'Content-Type':'application/json','Origin':base})
 try:
  with c.open(r,timeout=10) as f:code=f.status;data=f.read()
 except urllib.error.HTTPError as e:code=e.code;data=e.read()
 try:return code,json.loads(data)
 except ValueError:return code,data.decode()
def check(name,actual,expected):
 ok=actual==expected;records.append({'check':name,'actual':actual,'expected':expected,'pass':ok});out.write_text(json.dumps(records,indent=2));print(name,ok,flush=True)
 if not ok:raise AssertionError(name)
def login(name,pw):
 c=client();code,data=req(c,'POST','/api/signin',{'username':name,'password':pw});check('login '+name,code,200);return c
try:
 anon=client();check('anonymous list rejected',req(anon,'GET','/api/devs')[0],401)
 admin=login('admin',os.environ['RTTY_ADMIN_PASSWORD'])
 stamp=str(int(time.time()));pw='E2e!'+stamp+'Password'
 code,ta=req(admin,'POST','/api/manage/tenants',{'name':'A-'+stamp});check('create tenant A',code,201)
 code,tb=req(admin,'POST','/api/manage/tenants',{'name':'B-'+stamp});check('create tenant B',code,201)
 users={}
 for name,t,role in [('alice',ta,'admin'),('bob',tb,'admin'),('reader',ta,'viewer'),('operator',ta,'operator')]:
  name=name+stamp;code,u=req(admin,'POST','/api/manage/users',{'username':name,'password':pw});check('create user',code,201)
  check('add member',req(admin,'PUT','/api/manage/members',{'tenant_id':t['id'],'user_id':u['id'],'role':role})[0],200)
  users[name.rstrip('0123456789')]=(u,login(name,pw))
 a=users['alice'][1];b=users['bob'][1];reader=users['reader'][1];op=users['operator'][1]
 code,da=req(a,'POST','/api/manage/devices',{'wire_id':'same-device','group':'lab','name':'Device A','web_targets':['http://127.0.0.1:8080']});check('enroll A',code,201)
 code,db=req(b,'POST','/api/manage/devices',{'wire_id':'same-device','group':'lab','name':'Device B'});check('same wire ID other tenant',code,201)
 check('cross tenant device detail',req(b,'GET','/api/dev/'+da['id'])[0],404)
 check('cross tenant device mutation',req(b,'PATCH','/api/manage/devices/'+da['id'],{'disabled':True})[0],404)
 check('cross tenant member mutation',req(b,'PUT','/api/manage/members',{'tenant_id':ta['id'],'user_id':users['bob'][0]['id'],'role':'admin'})[0],403)
 check('tenant admin cannot create tenants',req(a,'POST','/api/manage/tenants',{'name':'illegal'})[0],403)
 check('viewer cannot enroll',req(reader,'POST','/api/manage/devices',{'wire_id':'illegal'})[0],403)
 check('viewer terminal denied',req(reader,'GET','/api/connect/'+da['id'])[0],403)
 check('ungranted operator denied',req(op,'GET','/api/connect/'+da['id'])[0],403)
 check('grant operator',req(a,'PUT','/api/manage/grants',{'user_id':users['operator'][0]['id'],'device_id':da['id'],'access':True})[0],200)
 check('commands disabled',req(a,'POST','/api/cmd/'+da['id'],{'cmd':'id','username':'root'})[0],403)
 check('shares disabled',req(a,'GET','/api/shares')[0],403)
 check('revoke member',req(a,'DELETE','/api/manage/members/'+users['operator'][0]['id'])[0],200)
 check('old login rejected after revoke',req(op,'GET','/api/me')[0],401)
 check('self password change',req(reader,'POST','/api/password',{'current_password':pw,'new_password':pw+'new'})[0],200)
 check('password change revokes old session',req(reader,'GET','/api/me')[0],401)
 check('old password rejected',req(client(),'POST','/api/signin',{'username':users['reader'][0]['username'],'password':pw})[0],401)
 # Credentials are an explicit private fixture for socket/device E2E, never printed.
 fixture=out.with_suffix('.fixture.json');fixture.write_text(json.dumps({'tenant_a':ta,'tenant_b':tb,'device_a':da,'device_b':db,'users':{k:{'id':v[0]['id'],'username':v[0]['username']} for k,v in users.items()},'password':pw}));fixture.chmod(0o600)
except Exception as e:
 records.append({'error':str(e),'pass':False});out.write_text(json.dumps(records,indent=2));sys.exit(1)
