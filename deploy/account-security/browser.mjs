// Own local Docker HTTPS cabinet and real Mailpit. No system trust or native VPN changes.
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
function mail(email,purpose){
 const code=`import importlib.util,json,re,sys
s=importlib.util.spec_from_file_location('local','deploy/acceptance/local.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
d=json.load(sys.stdin)
def proof():
 _,_,raw=m.request(m.session(),'https://localhost:59446/api/v1/messages')
 for row in json.loads(raw)['messages']:
  if any(r['Address']==d['email'] for r in row['To']):
   _,_,raw=m.request(m.session(),'https://localhost:59446/api/v1/message/'+row['ID'])
   text=json.loads(raw)['Text']
   if d['purpose'] in text:
    match=re.search(r'#token=([A-Za-z0-9_-]{43})',text)
    if match:return match[1]
print(json.dumps(m.wait_until(proof)))
`;
 return JSON.parse(execFileSync('python3',['-c',code],{cwd:root,input:JSON.stringify({email,purpose}),stdio:['pipe','pipe','pipe'],timeout:35000}).toString());
}
function local(action,data){
 const code=`import importlib.util,json,sys
s=importlib.util.spec_from_file_location('local','deploy/acceptance/local.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
d=json.load(sys.stdin)
if d['action']=='approve': print(json.dumps(m.approve({'request_id':d['request_id']})['operation_id']))
else:
 snap=m.snapshot(d['operation'])
 print(json.dumps({'snapshot':snap,'panel':m.panel_readback(snap['target'])}))
`;
 return JSON.parse(execFileSync('python3',['-c',code],{cwd:root,input:JSON.stringify({action,...data}),stdio:['pipe','pipe','pipe'],timeout:40000}).toString());
}
let browser,step='launch';
try{
 const all=process.argv[2]==='all';if(!all&&process.argv[2]!=='reset')throw new Error('unsupported scenario');
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 const context=await browser.newContext({viewport:{width:375,height:812}}),second=await browser.newContext();
 const page=await context.newPage(),other=await second.newPage();
 const email='security-'+randomUUID()+'@example.test',password='Local original '+randomUUID(),newPassword='Local changed '+randomUUID();
 step='register';await page.goto('https://localhost:58443/register?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);await page.getByRole('checkbox',{name:/terms of use/}).check();await page.getByRole('checkbox',{name:/privacy policy/}).check();await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the address is available, we will send an email.')).toBeVisible();
 const registration=mail(email,'/verify-email');step='verify';await page.goto('https://localhost:58443/verify-email?lang=en#token='+registration);await page.getByLabel('Password',{exact:true}).fill(password);await page.getByRole('button',{name:'Verify email',exact:true}).click();await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 async function login(p,pw,address=email){await p.goto('https://localhost:58443/login?lang=en');await p.getByLabel('Email',{exact:true}).fill(address);await p.getByLabel('Password',{exact:true}).fill(pw);await p.getByRole('button',{name:'Sign in',exact:true}).click();await expect(p).toHaveURL(/cabinet/);}
 step='old sessions';await login(page,password);await login(other,password);
 let operation,nativeBefore,keyBefore,owner;
 if(all){
  step='native trial';await page.getByRole('button',{name:'Request trial',exact:true}).click();await expect(page.getByText('Request sent. Waiting for support')).toBeVisible();
  const current=await page.evaluate(async()=>await(await fetch('/api/v1/trial-requests/current')).json());operation=local('approve',{request_id:current.request.request_id});await expect(page.getByText('Trial active',{exact:true})).toBeVisible({timeout:25000});
  nativeBefore=local('native',{operation});owner=await page.evaluate(async()=>(await(await fetch('/api/v1/me')).json()).account.account_id);
  await page.getByRole('button',{name:'Show subscription link'}).click();keyBefore=await page.getByLabel('Subscription link',{exact:true}).inputValue();
 }

 // Real wall-clock cooldown, without changing production rate limits or fixture Redis.
 step='mail cooldown';await new Promise(resolve=>setTimeout(resolve,61000));
 step='request';await page.goto('https://localhost:58443/forgot-password?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the account exists, we will send instructions.')).toBeVisible();
 const proof=mail(email,'/reset-password');step='explicit completion';await page.goto('https://localhost:58443/reset-password?lang=en#token='+proof);expect(new URL(page.url()).hash).toBe('');expect(await page.evaluate(()=>JSON.stringify([localStorage,sessionStorage]))).not.toContain(proof);await page.getByLabel('Password',{exact:true}).fill(newPassword);await page.getByLabel('Repeat password').fill(newPassword);await page.getByRole('button',{name:'Save password'}).click();await expect(page.getByText('Password changed. You can now sign in.')).toBeVisible();
 step='sessions revoked';for(const p of [page,other])expect(await p.evaluate(async()=>(await fetch('/api/v1/me')).status)).toBe(401);
 step='old password';await page.goto('https://localhost:58443/login?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);await page.getByLabel('Password',{exact:true}).fill(password);await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Sign in failed');
 step='new login';await login(page,newPassword);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 if(all){
  const finalPassword='Final browser '+randomUUID(),target='changed-'+randomUUID()+'@example.test';
  step='known password change';await login(other,newPassword);await page.goto('https://localhost:58443/cabinet/security?lang=en');await page.getByLabel('Current password',{exact:true}).fill(newPassword);await page.getByLabel('New password',{exact:true}).fill(finalPassword);await page.getByLabel('Repeat password').fill(finalPassword);
  let response=page.waitForResponse(r=>r.url().endsWith('/me/password-change'));await page.getByRole('button',{name:'Change password',exact:true}).click();expect((await response).status()).toBe(204);await expect(page.getByText('Saved.',{exact:true})).toBeVisible();expect(await other.evaluate(async()=>(await fetch('/api/v1/me')).status)).toBe(401);
  step='revoke other sessions';await login(other,finalPassword);await page.getByLabel('Current password',{exact:true}).fill(finalPassword);response=page.waitForResponse(r=>r.url().endsWith('/sessions/revoke-others'));await page.getByRole('button',{name:'End other sessions'}).click();expect((await response).status()).toBe(204);await expect(page.getByText('Saved.',{exact:true})).toBeVisible();expect(await other.evaluate(async()=>(await fetch('/api/v1/me')).status)).toBe(401);
  step='email mail cooldown';await new Promise(resolve=>setTimeout(resolve,61000));
  step='email pair request';await page.getByLabel('Current password',{exact:true}).fill(finalPassword);await page.getByLabel('New email').fill(target);response=page.waitForResponse(r=>r.url().endsWith('/me/email-change'));await page.getByRole('button',{name:'Change email',exact:true}).click();expect((await response).status()).toBe(202);await expect(page.getByText('Current email: awaiting confirmation')).toBeVisible();
  const oldProof=mail(email,'/confirm-email-change'),newProof=mail(target,'/confirm-email-change');
  step='first mailbox';await page.goto('https://localhost:58443/confirm-email-change?lang=en#token='+oldProof);expect(new URL(page.url()).hash).toBe('');response=page.waitForResponse(r=>r.url().endsWith('/email-change/confirm'));await page.getByRole('button',{name:'Confirm mailbox',exact:true}).click();expect((await response).status()).toBe(200);await expect(page.getByText('Confirm the other mailbox to finish the change.')).toBeVisible();
  step='second mailbox, separate context';await other.goto('https://localhost:58443/confirm-email-change?lang=en#token='+newProof);expect(new URL(other.url()).hash).toBe('');response=other.waitForResponse(r=>r.url().endsWith('/email-change/confirm'));await other.getByRole('button',{name:'Confirm mailbox',exact:true}).click();expect((await response).status()).toBe(200);await expect(other.getByText('Email changed. Sign in with the new address.')).toBeVisible();expect(await page.evaluate(async()=>(await fetch('/api/v1/me')).status)).toBe(401);
  step='new email login and ownership';await login(page,finalPassword,target);expect(await page.evaluate(async()=>(await(await fetch('/api/v1/me')).json()).account.account_id)).toBe(owner);await page.getByRole('button',{name:'Show subscription link'}).click();expect(await page.getByLabel('Subscription link',{exact:true}).inputValue()).toBe(keyBefore);expect(local('native',{operation})).toEqual(nativeBefore);
  step='final logout';await page.getByRole('button',{name:'Sign out'}).click();await expect(page).toHaveURL(/login/);expect(await page.evaluate(async()=>(await fetch('/api/v1/me')).status)).toBe(401);
  console.log('PASS: real Docker reset/password/session/email/logout UI, both TLS Mailpit mailboxes, separate contexts, same owner/subscription/native 3X-UI3.7.0 target and limits');
 }else console.log('PASS: real Docker HTTPS reset link via TLS Mailpit/River, two sessions revoked, old password rejected, new login and mobile form');
}catch{
 console.error('FAIL: Account security Docker browser step '+step);process.exitCode=1;
}finally{await browser?.close();}
