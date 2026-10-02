// Subscription/Device connection acceptance against the owned HTTPS Docker cabinet and native 3X-UI.
// Secrets cross the Python bridge only on stdin; output contains booleans and counters.
import {createRequire} from 'node:module';
import {readFileSync} from 'node:fs';
import {createHash,randomUUID,X509Certificate} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

const root=fileURLToPath(new URL('../../',import.meta.url));
const ownedProject=JSON.parse(readFileSync(root+'.superpowers/acceptance/local-docker/runtime.json')).project;
if(typeof ownedProject!=='string'||!ownedProject)throw Error('owned project metadata missing');
const require=createRequire(root+'web/package.json');
const {chromium,expect}=require('@playwright/test');
const cert=new X509Certificate(readFileSync(root+'.superpowers/acceptance/local-docker/cert.pem'));
const pin=createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64');
const python=`import importlib.util,json,sys
from uuid import UUID
from urllib.parse import urlsplit
import urllib.request
from urllib.error import HTTPError
s=importlib.util.spec_from_file_location('local','deploy/acceptance/local.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
d=json.load(sys.stdin)
if d['action']=='token':
 print(json.dumps(m.wait_until(lambda:m.mail_token(d['email']))))
elif d['action']=='approve':
 print(json.dumps(m.approve({'request_id':d['request_id']})['operation_id']))
else:
 class OwnSession:
  def __init__(self,cookie):
   self.cookie=cookie
   self.opener=m.session()
  def open(self,req,timeout=15):
   req.add_unredirected_header('Cookie',self.cookie)
   return self.opener.open(req,timeout=timeout)
 def own_api(cookie,path):
  try: return m.api(OwnSession(cookie),path)[0:3:2]
  except HTTPError as error: return error.code,None
 def own_client(target):
  panel,csrf=m.login_panel()
  obj=m.panel_call(panel,'panel/api/clients/get/'+target['panel_key'])
  client=obj['client']
  assert client['uuid']==target['vpn_id'] and client['subId']==target['sub_id']
  assert sorted(obj['inboundIds'])==sorted(target['inbound_ids'])
  return panel,csrf,client,obj['inboundIds']
 def panel_ready():
  try:return bool(m.panel_call(m.session(),'csrf-token'))
  except (OSError,ValueError):return False
 def payload(client,changes):
  # Native3.7.0 binds model.Client; its id is UUID, while readback id is the DB integer.
  result={**client,'id':client['uuid'],'created_at':client['createdAt'],**changes}
  result['allowedIPs']=[ip.strip() for ip in client['allowedIPs'].split(',') if ip.strip()]
  if isinstance(result.get('reverse'),str):result['reverse']=json.loads(result['reverse']) if result['reverse'].strip() else None
  return result
 def same_client(target,before):
  _,_,after,ids=own_client(target)
  return all(after.get(field)==value for field,value in before.items() if field!='updatedAt') and sorted(ids)==sorted(target['inbound_ids'])
 def own_sql(statement,binding=None):
  if binding is not None:statement=chr(92)+"set own_uuid '"+str(UUID(binding))+"'"+chr(10)+statement
  return m.compose('exec','-T','postgres','psql','-U',m.PG_USER,'-d',m.database(),'-qAt','-v','ON_ERROR_STOP=1',stdin=statement.encode()).decode().strip()
 def own_snapshot(operation):
  return json.loads(own_sql("SELECT json_build_object('target',o.target,'account_id',o.account_id,'status',o.status,'grant',g.status,'grants',(SELECT count(*) FROM trial_grants WHERE operation_id=o.id)) FROM trial_operations o JOIN trial_grants g ON g.operation_id=o.id WHERE o.id=:'own_uuid'::uuid;",operation))
 snap=own_snapshot(d['operation']);target=snap['target']
 assert snap['status']=='applied' and snap['grant']=='granted' and snap['grants']==1
 if d['action']=='vpn':
  path=m.STATE/'vpn.json';original=path.read_bytes();prior=m.vpn_connected();assert prior
  connected=False;restored=False;phase='key';failure='none'
  try:
   status,_=own_api(d['cookie'],'/api/v1/subscription/key');failure='key-'+str(status);assert status==200
   phase='client'
   m.vpn(OwnSession(d['cookie']))
   connected=True
  except Exception as error:
   failure=phase+'-'+type(error).__name__
  finally:
   try:
    path.write_bytes(original);path.chmod(0o600)
    m.compose('--profile','vpn','up','--no-build','--pull','never','--force-recreate','-d','vpn-client')
    restored=m.wait_until(m.vpn_connected)
   except Exception: pass
  print(json.dumps({'vpn':connected,'prior_restored':restored,'failure':failure}))
 elif d['action']=='state':
  case=d['case'];cookie=d['cookie'];panel,csrf,before,ids=own_client(target)
  assert m.panel_readback(target)['enable'] is True
  status,base=own_api(cookie,'/api/v1/subscription');assert status==200 and base['status']=='active' and base['data_stale'] is False
  changed=False;restored=False;result={};phase='mutate'
  try:
   if case=='ban':
    account=str(UUID(d['account']));assert account==snap['account_id']
    previous=own_sql("SELECT vpn_banned FROM accounts WHERE id=:'own_uuid'::uuid;",account)
    assert previous=='f'
    changed=True
    own_sql("UPDATE accounts SET vpn_banned=true WHERE id=:'own_uuid'::uuid;",account)
   else:
    traffic=m.panel_call(panel,'panel/api/clients/traffic/'+target['panel_key'])
    assert traffic['email']==target['panel_key'] and traffic['uuid']==target['vpn_id'] and traffic['subId']==target['sub_id']
    used=traffic['up']+traffic['down'];assert used>0 or case=='unlimited'
    changes={'disabled':{'enable':False},'expired':{'expiryTime':1},'exhausted':{'totalGB':max(1,used-1)},'unlimited':{'limitIp':0,'totalGB':0,'expiryTime':0}}[case]
    phase='panel-update'
    changed=True
    m.panel_call(panel,'panel/api/clients/update/'+target['panel_key'],payload(before,changes),csrf)
    phase='native-readback'
    _,_,updated,members=own_client(target)
    assert sorted(members)==sorted(ids) and updated['uuid']==before['uuid'] and updated['subId']==before['subId']
    assert all(updated[field]==value for field,value in changes.items())
   phase='subscription'
   status,view=own_api(cookie,'/api/v1/subscription');assert status==200 and view['data_stale'] is False
   phase='key'
   key_status,key=own_api(cookie,'/api/v1/subscription/key')
   result={'status':view['status'],'key_status':key_status,'identity':True,'grant_count':own_snapshot(d['operation'])['grants']}
   if case=='ban':
    result['profile_preserved']=view['access_profile']==base['access_profile'] and view['devices']==base['devices'] and view['traffic_limit_bytes']==base['traffic_limit_bytes']
   if case=='expired':result['key_own']=key_status==200 and urlsplit(key['subscription_url']).path=='/sub/'+target['sub_id']
   if case=='exhausted':result['remaining_zero']=view['traffic_remaining_bytes']==0
   if case=='unlimited':
    result['unlimited']=view['unlimited_devices'] is True and view['unlimited_traffic'] is True and view['devices']==0 and view.get('traffic_remaining_bytes') is None and view.get('expires_at') is None and view['traffic_upload_bytes'] is not None and view['traffic_download_bytes'] is not None and view['observed_at'] is not None
  except Exception as error:
   result={'bridge_error':phase+'-'+type(error).__name__}
  finally:
   if changed:
    if case=='ban':
     own_sql("UPDATE accounts SET vpn_banned="+{'t':'true','f':'false'}[previous]+" WHERE id=:'own_uuid'::uuid;",account)
     assert own_sql("SELECT vpn_banned FROM accounts WHERE id=:'own_uuid'::uuid;",account)==previous
    else:m.panel_call(panel,'panel/api/clients/update/'+target['panel_key'],payload(before,{}),csrf)
   restored=same_client(target,before) and m.panel_readback(target)['enable'] is True
   status,recovered=own_api(cookie,'/api/v1/subscription')
   restored=restored and status==200 and recovered['status']=='active' and recovered['data_stale'] is False and own_snapshot(d['operation'])['grants']==1 and own_snapshot(d['operation'])['target']==target
  result['restored']=restored
  print(json.dumps(result))
 elif d['action']=='outage':
  cookie=d['cookie'];_,_,before,_=own_client(target)
  status,fresh=own_api(cookie,'/api/v1/subscription');assert status==200 and fresh['status']=='active' and fresh['data_stale'] is False
  stopped=False;result={}
  try:
   m.compose('stop','panel');stopped=True
   status,stale=own_api(cookie,'/api/v1/subscription');key_status,_=own_api(cookie,'/api/v1/subscription/key')
   fields=('status','devices','traffic_limit_bytes','traffic_used_bytes','traffic_upload_bytes','traffic_download_bytes','traffic_remaining_bytes','unlimited_traffic','unlimited_devices','access_profile','expires_at','observed_at')
   result={'stale':status==200 and stale['data_stale'] is True and stale['panel_error']=='unavailable' and stale['connection_available'] is False and all(stale.get(field)==fresh.get(field) for field in fields),'key_status':key_status}
  finally:
   if stopped:
    m.compose('up','--no-build','--pull','never','--no-deps','-d','panel')
    m.wait_until(panel_ready,timeout=40)
    m.wait_until(m.vpn_connected,timeout=30)
   status,recovered=own_api(cookie,'/api/v1/subscription')
   result['restored']=status==200 and recovered['status']=='active' and recovered['data_stale'] is False and same_client(target,before) and m.panel_readback(target)['enable'] is True and m.vpn_connected() and own_snapshot(d['operation'])['grants']==1 and own_snapshot(d['operation'])['target']==target
  print(json.dumps(result))
 else:
  panel=m.panel_readback(target)
  opener,_=m.login_panel()
  traffic=m.panel_call(opener,'panel/api/clients/traffic/'+target['panel_key'])
  assert traffic['email']==target['panel_key'] and traffic['uuid']==target['vpn_id'] and traffic['subId']==target['sub_id']
  assert panel['uuid']==target['vpn_id'] and panel['subId']==target['sub_id']
  assert traffic['up']>=0 and traffic['down']>=0
  key_ok=urlsplit(d['key']).scheme=='https' and urlsplit(d['key']).path=='/sub/'+target['sub_id']
  print(json.dumps({'up':traffic['up'],'down':traffic['down'],'devices':target['device_count'],'limit':target['traffic_limit_bytes'],'expiry':target['expiry_time_ms'],'key_ok':key_ok,'grant_count':snap['grants'],'panel_identity':True}))
`;
function local(action,data){
 try{return JSON.parse(execFileSync('python3',['-c',python],{cwd:root,input:JSON.stringify({action,...data}),stdio:['pipe','pipe','pipe'],timeout:90000}).toString());}
 catch(error){const matches=error.stderr?.toString().match(/(?:AssertionError|RuntimeError|HTTPError|KeyError|TypeError|ValueError|TimeoutError)/g);throw new Error('state bridge-'+(matches?.at(-1)||'unclassified'));}
}
const origin='https://localhost:58443';
let browser,step='launch';
try{
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 const context=await browser.newContext({viewport:{width:375,height:812}}),page=await context.newPage();
 const email='device-connection-'+randomUUID()+'@example.test',password='Local Device connection '+randomUUID();
 step='register';await page.goto(origin+'/register?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByRole('checkbox',{name:/terms of use/}).check();await page.getByRole('checkbox',{name:/privacy policy/}).check();
 await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the address is available, we will send an email.')).toBeVisible();
 const token=local('token',{email});step='verify';await page.goto(origin+'/verify-email?lang=en#token='+token);
 expect(new URL(page.url()).hash).toBe('');await page.getByLabel('Password',{exact:true}).fill(password);
 await page.getByRole('button',{name:'Verify email',exact:true}).click();await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 step='login';await page.getByRole('link',{name:'Sign in',exact:true}).first().click();await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByLabel('Password',{exact:true}).fill(password);await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page).toHaveURL(/cabinet/);
 expect((await page.evaluate(async()=>(await fetch('/api/v1/subscription')).json())).status).toBe('none');
 expect(await page.evaluate(async()=>(await fetch('/api/v1/subscription/key')).status)).toBe(409);
 step='trial';await page.getByRole('button',{name:'Request trial',exact:true}).click();await expect(page.getByText('Request sent. Waiting for support')).toBeVisible();
 const current=await page.evaluate(async()=>await(await fetch('/api/v1/trial-requests/current')).json());
 const operation=local('approve',{request_id:current.request.request_id});await expect(page.getByText('Trial active',{exact:true})).toBeVisible({timeout:30000});
 step='profile';let sub=await page.evaluate(async()=>await(await fetch('/api/v1/subscription')).json());
 expect(sub.status).toBe('active');expect(sub.access_profile).toBe('regular');expect(sub.devices).toBe(1);
 expect(sub.traffic_limit_bytes).toBe(15*1024**3);expect(sub.traffic_upload_bytes).toBeGreaterThanOrEqual(0);
 expect(sub.traffic_download_bytes).toBeGreaterThanOrEqual(0);expect(sub.traffic_used_bytes).toBe(sub.traffic_upload_bytes+sub.traffic_download_bytes);
 expect(sub.traffic_remaining_bytes).toBe(Math.max(0,sub.traffic_limit_bytes-sub.traffic_used_bytes));expect(sub.observed_at).toBeTruthy();
 expect(sub.data_stale).toBe(false);expect(sub.connection_available).toBe(true);expect(sub.expires_at).toBeTruthy();
 await expect(page.getByText('Trial active',{exact:true})).toBeVisible();await expect(page.getByText('Upload',{exact:true})).toBeVisible();
 await expect(page.getByText('Download',{exact:true})).toBeVisible();await expect(page.getByText('Remaining',{exact:true})).toBeVisible();
 step='platforms';const platforms=[['ios','https://apps.apple.com/us/app/happ-proxy-utility/id6504287215'],['android','https://play.google.com/store/apps/details?id=com.happproxy'],['macos','https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.macOS.universal.dmg'],['windows','https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.x64.exe'],['other','https://www.happ.su/main']];
 const selector=page.getByLabel('Platform');for(const [platform,href] of platforms){await selector.selectOption(platform);const install=page.getByRole('link',{name:platform==='other'?'Open developer catalog':'Install Happ'});await expect(install).toHaveAttribute('href',href);await expect(install).toHaveAttribute('rel',/noreferrer.*noopener/);}
 await selector.selectOption('ios');await page.getByLabel('App Store region').selectOption('ru');await expect(page.getByRole('link',{name:'Install Happ'})).toHaveAttribute('href','https://apps.apple.com/ru/app/happ-lite/id6799917773');
 await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByRole('heading',{name:'Подключить устройство'})).toBeVisible();await expect(page.getByLabel('Регион App Store')).toHaveValue('ru');
 await page.getByRole('button',{name:'EN',exact:true}).click();await expect(page.getByLabel('App Store region')).toHaveValue('ru');await selector.focus();await page.keyboard.press('m');await expect(selector).toHaveValue('macos');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await expect(page.getByRole('list',{name:'Connection steps'}).getByRole('listitem')).toHaveCount(3);
 step='key';const response=page.waitForResponse(r=>r.url().endsWith('/subscription/key'));await page.getByRole('button',{name:'Show subscription link'}).click();
 const keyResponse=await response;expect(keyResponse.status()).toBe(200);expect(keyResponse.headers()['cache-control']).toContain('no-store');
 const key=await page.getByLabel('Subscription link',{exact:true}).inputValue();expect(key.startsWith('https://localhost:59445/sub/')).toBe(true);
 step='foreign hint';
 const foreignHint=await page.evaluate(async id=>{const response=await fetch('/api/v1/subscription/key?account_id='+encodeURIComponent(id));return {status:response.status,body:await response.json()};},randomUUID());
 expect([200,400]).toContain(foreignHint.status);if(foreignHint.status===200)expect(foreignHint.body.subscription_url).toBe(key);
 const cookie=(await context.cookies()).find(c=>c.name==='__Host-session');expect(cookie?.httpOnly&&cookie.secure).toBe(true);
 let native=local('native',{operation,key});expect(native.key_ok&&native.panel_identity).toBe(true);expect(native.grant_count).toBe(1);
 expect(native.expiry).toBe(Date.parse(sub.expires_at));expect(native.devices).toBe(sub.devices);expect(native.limit).toBe(sub.traffic_limit_bytes);
 step='copy and QR';await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:(value)=>{window.__copied=value;return Promise.resolve();}}}));
 await page.getByRole('button',{name:'Copy'}).click();expect(await page.evaluate(()=>window.__copied)).toBe(key);
 const external=[];page.on('request',request=>{if(!request.url().startsWith(origin))external.push(request.url());});
 await page.getByRole('button',{name:'Show QR code'}).click();await expect(page.getByRole('img',{name:'Subscription QR code'})).toBeVisible();
 const canvas=page.getByRole('img',{name:'Subscription QR code'});expect(await canvas.evaluate(node=>node.width>0&&node.height>0&&!!node.getContext('2d'))).toBe(true);
 expect(external).toHaveLength(0);
 step='deep link';const deep=page.getByRole('link',{name:'Open in Happ'});await expect(deep).toHaveAttribute('href','happ://add/'+key);
 await page.evaluate(()=>document.addEventListener('click',event=>{if(event.target instanceof Element&&event.target.closest('a[href^="happ:"]'))event.preventDefault();},{capture:true}));
 const before=page.url();await deep.click();expect(page.url()).toBe(before);
 step='hide';await page.getByRole('button',{name:'Hide connection details'}).click();await expect(page.getByLabel('Subscription link',{exact:true})).toHaveCount(0);await expect(canvas).toHaveCount(0);
 step='VPN browser key';expect(await page.evaluate(async()=>(await fetch('/api/v1/subscription/key')).status)).toBe(200);
 step='VPN';const vpn=local('vpn',{operation,cookie:cookie.name+'='+cookie.value});if(!vpn.vpn||!vpn.prior_restored)throw new Error('vpn '+(vpn.vpn?'restore':vpn.failure));
 step='traffic';let matched=false;for(let attempt=0;attempt<20;attempt++){
  sub=await page.evaluate(async()=>await(await fetch('/api/v1/subscription',{cache:'no-store'})).json());
  native=local('native',{operation,key});
  if(sub.traffic_upload_bytes===native.up&&sub.traffic_download_bytes===native.down&&sub.traffic_used_bytes===native.up+native.down&&native.up>0&&native.down>0){matched=true;break;}
  await new Promise(resolve=>setTimeout(resolve,500));
 }
 expect(matched).toBe(true);expect(sub.traffic_remaining_bytes).toBe(Math.max(0,sub.traffic_limit_bytes-native.up-native.down));
 expect(sub.connection_available).toBe(true);expect(native.grant_count).toBe(1);
 step='second registration';const secondContext=await browser.newContext(),second=await secondContext.newPage();
 const secondEmail='device-connection-other-'+randomUUID()+'@example.test',secondPassword='Local Device connection '+randomUUID();
 await second.goto(origin+'/register?lang=en');await second.getByLabel('Email',{exact:true}).fill(secondEmail);
 await second.getByRole('checkbox',{name:/terms of use/}).check();await second.getByRole('checkbox',{name:/privacy policy/}).check();
 await second.getByRole('button',{name:'Continue',exact:true}).click();await expect(second.getByText('If the address is available, we will send an email.')).toBeVisible();
 step='second verify';const secondToken=local('token',{email:secondEmail});await second.goto(origin+'/verify-email?lang=en#token='+secondToken);
 await second.getByLabel('Password',{exact:true}).fill(secondPassword);await second.getByRole('button',{name:'Verify email',exact:true}).click();
 await expect(second.getByText('Email verified. You can now sign in.')).toBeVisible();
 step='second login';await second.getByRole('link',{name:'Sign in',exact:true}).first().click();
 await second.getByLabel('Email',{exact:true}).fill(secondEmail);await second.getByLabel('Password',{exact:true}).fill(secondPassword);
 await second.getByRole('button',{name:'Sign in',exact:true}).click();await expect(second).toHaveURL(/cabinet/);
 const firstID=await page.evaluate(async()=>(await(await fetch('/api/v1/me')).json()).account.account_id);
 const secondID=await second.evaluate(async()=>(await(await fetch('/api/v1/me')).json()).account.account_id);expect(secondID).not.toBe(firstID);
 expect((await second.evaluate(async()=>(await fetch('/api/v1/subscription')).json())).status).toBe('none');
 expect(await second.evaluate(async()=>(await fetch('/api/v1/subscription/key')).status)).toBe(409);
 step='second trial';await second.getByRole('button',{name:'Request trial',exact:true}).click();await expect(second.getByText('Request sent. Waiting for support')).toBeVisible();
 const secondCurrent=await second.evaluate(async()=>await(await fetch('/api/v1/trial-requests/current')).json());
 const secondOperation=local('approve',{request_id:secondCurrent.request.request_id});await expect(second.getByText('Trial active',{exact:true})).toBeVisible({timeout:30000});
 await second.getByRole('button',{name:'Show subscription link'}).click();
 const secondKey=await second.getByLabel('Subscription link',{exact:true}).inputValue();expect(secondKey).not.toBe(key);
 const secondNative=local('native',{operation:secondOperation,key:secondKey});expect(secondNative.key_ok&&secondNative.panel_identity&&secondNative.grant_count===1).toBe(true);
 step='foreign account guards';for(const [customer,foreignID,ownKey] of [[page,secondID,key],[second,firstID,secondKey]]){
  const check=await customer.evaluate(async id=>{const response=await fetch('/api/v1/subscription/key?account_id='+encodeURIComponent(id));return {status:response.status,body:await response.json()};},foreignID);
  expect([200,400]).toContain(check.status);if(check.status===200)expect(check.body.subscription_url).toBe(ownKey);
 }
 const firstCookie=cookie.name+'='+cookie.value;
 for(const [name,want,keyStatus] of [['disabled','disabled',409],['expired','expired',200],['exhausted','exhausted',409],['unlimited','active',200],['ban','banned',403]]){
  step='native '+name;
  let state;try{state=local('state',{case:name,operation,cookie:firstCookie,account:firstID});}catch(error){throw error;}
  if(state.bridge_error)throw new Error('state '+state.bridge_error);
  step='native '+name+' response';
  expect(state.status).toBe(want);expect(state.key_status).toBe(keyStatus);expect(state.identity&&state.restored&&state.grant_count===1).toBe(true);
  if(name==='expired')expect(state.key_own).toBe(true);
  if(name==='exhausted')expect(state.remaining_zero).toBe(true);
  if(name==='unlimited')expect(state.unlimited).toBe(true);
  if(name==='ban')expect(state.profile_preserved).toBe(true);
  console.log('PASS: native '+name+' / key '+state.key_status+' / full client restored / one Grant');
 }
 console.log('PASS: two real owner contexts / distinct own native keys / reciprocal foreign query cannot select foreign key');
 step='panel outage';const outage=local('outage',{operation,cookie:firstCookie});
 expect(outage.stale&&outage.key_status===409&&outage.restored).toBe(true);
 console.log('PASS: actual own panel stop / full cached profile and observed_at / stale / key409 / start and fresh recovery / prior VPN healthy');
 step='native restoration';const finalNative=local('native',{operation,key});expect(finalNative.key_ok&&finalNative.panel_identity&&finalNative.grant_count===1).toBe(true);
 expect(finalNative.devices).toBe(native.devices);expect(finalNative.limit).toBe(native.limit);expect(finalNative.expiry).toBe(native.expiry);
 await page.reload();await expect(page.getByText('Trial active',{exact:true})).toBeVisible();
 step='logout';await page.getByRole('button',{name:'Sign out'}).click();await expect(page).toHaveURL(/login/);
 expect(await page.evaluate(async()=>(await fetch('/api/v1/subscription/key')).status)).toBe(401);
 await second.getByRole('button',{name:'Sign out'}).click();await expect(second).toHaveURL(/login/);
 expect(await second.evaluate(async()=>(await fetch('/api/v1/subscription/key')).status)).toBe(401);
 console.log('PASS: two real browser owners/distinct native keys and foreign query guard; native disabled/expired/exhausted/unlimited/ban/outage states and keys restored; positive split traffic/prior Docker VPN; Device connection controls, no-store and both logouts');
}catch(error){const detail=String(error?.message);console.error('FAIL: Subscription/Device connection native browser step '+step+(detail.startsWith('vpn ')||detail.startsWith('state ')?' '+detail:''));process.exitCode=1;}finally{await browser?.close();}
