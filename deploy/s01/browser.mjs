// Actual local cabinet + Mailpit + shipped adapter + native panel, without Telegram.
import {createRequire} from 'node:module';
import {readFileSync} from 'node:fs';
import {createHash,randomUUID,X509Certificate} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../../',import.meta.url));
const require=createRequire(root+'web/package.json');
const {chromium,expect}=require('@playwright/test');
const cert=new X509Certificate(readFileSync(root+'.superpowers/sdd/2026-10-01-s01-web-trial/local-docker/cert.pem'));
const pin=createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64');
function local(action,data){
 const code=`import importlib.util,json,sys
s=importlib.util.spec_from_file_location('local','deploy/s01/local.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
d=json.load(sys.stdin)
if d['action']=='token': print(json.dumps(m.wait_until(lambda:m.mail_token(d['email']))))
else: m.approve({'request_id':d['request_id']});print('true')
`;
 return JSON.parse(execFileSync('python3',['-c',code],{cwd:root,input:JSON.stringify({action,...data}),stdio:['pipe','pipe','pipe'],timeout:40000}).toString());
}
let browser,step='launch';
try{
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 const context=await browser.newContext({viewport:{width:375,height:812}});
 const page=await context.newPage();
 const email='browser-'+randomUUID()+'@example.test',password='Local browser '+randomUUID();
 step='register';await page.goto('https://localhost:58443/register?lang=en');
 await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByRole('checkbox',{name:/terms of use/}).check();await page.getByRole('checkbox',{name:/privacy policy/}).check();
 await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the address is available, an email has been sent.')).toBeVisible();
 const token=local('token',{email});step='verify';
 await page.goto('https://localhost:58443/verify-email?lang=en#token='+token);expect(new URL(page.url()).hash).toBe('');
 await page.getByLabel('Password',{exact:true}).fill(password);await page.getByRole('button',{name:'Verify email',exact:true}).click();
 await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 step='login';await page.getByRole('link',{name:'Sign in',exact:true}).first().click();
 await page.getByLabel('Email',{exact:true}).fill(email);await page.getByLabel('Password',{exact:true}).fill(password);
 await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page).toHaveURL(/cabinet/);
 step='trial';await page.getByRole('button',{name:'Request trial',exact:true}).click();await expect(page.getByText('Request sent. Waiting for support')).toBeVisible();
 const current=await page.evaluate(async()=>await (await fetch('/api/v1/trial-requests/current')).json());
 local('approve',{request_id:current.request.request_id});await expect(page.getByText('Trial active',{exact:true})).toBeVisible({timeout:20000});
 step='key';const response=page.waitForResponse(r=>r.url().endsWith('/subscription/key'));
 await page.getByRole('button',{name:'Show subscription link'}).click();expect((await response).headers()['cache-control'].split(',').map(v=>v.trim())).toEqual(expect.arrayContaining(['no-store']));
 step='key shape';await expect(page.getByLabel('Subscription link',{exact:true})).toHaveValue(/^https:\/\/localhost:59445\/sub\/[a-z0-9]{16}$/);
 step='storage';expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toContain(token);
 step='cookie';const cookie=(await context.cookies()).find(c=>c.name==='__Host-session');expect(cookie?.httpOnly&&cookie.secure).toBe(true);
 step='overflow';expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 step='logout';await page.getByRole('button',{name:'Sign out'}).click();await expect(page).toHaveURL(/login/);
 expect(await page.evaluate(async()=>(await fetch('/api/v1/subscription/key')).status)).toBe(401);
 console.log('PASS: mobile browser registration, real Mailpit verification, login, adapter decision, native-panel trial/key, no-store and logout; Telegram transport not tested');
}catch{
 console.error('FAIL: local browser step '+step);process.exitCode=1;
}finally{await browser?.close();}
