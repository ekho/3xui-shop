// Own local Docker HTTPS cabinet and real Mailpit. No system trust or native VPN changes.
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
function mail(email,purpose){
 const code=`import importlib.util,json,re,sys
s=importlib.util.spec_from_file_location('local','deploy/s01/local.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
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
let browser,step='launch';
try{
 if(process.argv[2]!=='reset')throw new Error('unsupported scenario');
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 const context=await browser.newContext({viewport:{width:375,height:812}}),second=await browser.newContext();
 const page=await context.newPage(),other=await second.newPage();
 const email='security-'+randomUUID()+'@example.test',password='Local original '+randomUUID(),newPassword='Local changed '+randomUUID();
 step='register';await page.goto('https://localhost:58443/register?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);await page.getByRole('checkbox',{name:/terms of use/}).check();await page.getByRole('checkbox',{name:/privacy policy/}).check();await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the address is available, we will send an email.')).toBeVisible();
 const registration=mail(email,'/verify-email');step='verify';await page.goto('https://localhost:58443/verify-email?lang=en#token='+registration);await page.getByLabel('Password',{exact:true}).fill(password);await page.getByRole('button',{name:'Verify email',exact:true}).click();await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 async function login(p,pw){await p.goto('https://localhost:58443/login?lang=en');await p.getByLabel('Email',{exact:true}).fill(email);await p.getByLabel('Password',{exact:true}).fill(pw);await p.getByRole('button',{name:'Sign in',exact:true}).click();await expect(p).toHaveURL(/cabinet/);}
 step='old sessions';await login(page,password);await login(other,password);
 // Real wall-clock cooldown, without changing production rate limits or fixture Redis.
 step='mail cooldown';await new Promise(resolve=>setTimeout(resolve,61000));
 step='request';await page.goto('https://localhost:58443/forgot-password?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the account exists, we will send instructions.')).toBeVisible();
 const proof=mail(email,'/reset-password');step='explicit completion';await page.goto('https://localhost:58443/reset-password?lang=en#token='+proof);expect(new URL(page.url()).hash).toBe('');expect(await page.evaluate(()=>JSON.stringify([localStorage,sessionStorage]))).not.toContain(proof);await page.getByLabel('Password',{exact:true}).fill(newPassword);await page.getByLabel('Repeat password').fill(newPassword);await page.getByRole('button',{name:'Save password'}).click();await expect(page.getByText('Password changed. You can now sign in.')).toBeVisible();
 step='sessions revoked';for(const p of [page,other])expect(await p.evaluate(async()=>(await fetch('/api/v1/me')).status)).toBe(401);
 step='old password';await page.goto('https://localhost:58443/login?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);await page.getByLabel('Password',{exact:true}).fill(password);await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Sign in failed');
 step='new login';await login(page,newPassword);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 console.log('PASS: real Docker HTTPS reset link via TLS Mailpit/River, two sessions revoked, old password rejected, new login and mobile form');
}catch{
 console.error('FAIL: S02 Docker browser step '+step);process.exitCode=1;
}finally{await browser?.close();}
