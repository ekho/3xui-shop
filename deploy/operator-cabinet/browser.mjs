// Real Support/Operator cabinet browser and HTTP checks against the owned HTTPS Docker stack.
// Secrets and test identities stay in process memory; JSONL contains redacted verdicts.
import {createRequire} from 'node:module';
import {readFileSync,mkdirSync,openSync,writeSync,closeSync,chmodSync} from 'node:fs';
import {createHash,randomUUID,X509Certificate} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import https from 'node:https';
import {fileURLToPath} from 'node:url';

const root=fileURLToPath(new URL('../../',import.meta.url));
const ownedProject=JSON.parse(readFileSync(root+'.superpowers/acceptance/local-docker/runtime.json')).project;
if(typeof ownedProject!=='string'||!ownedProject)throw Error('owned project metadata missing');
const require=createRequire(root+'web/package.json');
const {chromium,expect}=require('@playwright/test');
const origin='https://localhost:58443';
const certPem=readFileSync(root+'.superpowers/acceptance/local-docker/cert.pem');
const cert=new X509Certificate(certPem);
const pin=createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64');
const evidence=root+'.superpowers/acceptance/operator-cabinet';
mkdirSync(evidence,{recursive:true,mode:0o700});
chmodSync(evidence,0o700);
const evidencePath=evidence+'/operator-cabinet-native-browser.jsonl';
const evidenceFd=openSync(evidencePath,'wx',0o600);
chmodSync(evidencePath,0o600);
const command='node deploy/operator-cabinet/browser.mjs';
function bridge(action,data={}){
 return JSON.parse(execFileSync('python3',['deploy/operator-cabinet/local.py'],{cwd:root,input:JSON.stringify({action,...data}),stdio:['pipe','pipe','pipe'],timeout:action==='restore'?600000:90000}).toString());
}
function mailJSON(path){
 return new Promise((resolve,reject)=>{
  https.get('https://localhost:59446'+path,{ca:certPem},response=>{
   const chunks=[];response.on('data',chunk=>chunks.push(chunk));
   response.on('end',()=>{try{if(response.statusCode!==200)throw new Error('Mailpit status');resolve(JSON.parse(Buffer.concat(chunks).toString()));}catch(error){reject(error);}});
  }).on('error',reject);
 });
}
async function mailToken(email){
 for(let attempt=0;attempt<150;attempt++){
  const messages=await mailJSON('/api/v1/messages');
  for(const row of messages.messages??[]){
   if(!row.To?.some(recipient=>recipient.Address===email))continue;
   const detail=await mailJSON('/api/v1/message/'+encodeURIComponent(row.ID));
   const token=detail.Text?.match(/#token=([A-Za-z0-9_-]{43})/);
   if(token)return token[1];
  }
  await new Promise(resolve=>setTimeout(resolve,200));
 }
 throw new Error('mail token timeout');
}
function genuineTelegramId(){
 const lines=readFileSync(root+'.env','utf8').split(/\r?\n/);
 const values=lines.filter(line=>line.startsWith('ADMIN_TG_ID=')).map(line=>line.slice('ADMIN_TG_ID='.length).trim().replace(/^['"]|['"]$/g,''));
 if(values.length!==1||!(/^[1-9][0-9]{0,18}$/).test(values[0])||BigInt(values[0])>9223372036854775807n)throw new Error('owner ID unavailable');
 return values[0];
}
function check(criterion,expected,actual,pass){
 const row={criterion,target:`${ownedProject} HTTPS/PG/Redis/Mailpit/native 3X-UI 3.7.0; browser customer and operator sessions`,command,expected,actual,verdict:pass?'PASS':'FAIL',artifacts:[evidencePath]};
 writeSync(evidenceFd,JSON.stringify(row)+'\n');
 if(!pass)throw new Error(criterion);
}
function blocked(criterion,expected,actual){
 writeSync(evidenceFd,JSON.stringify({criterion,target:`${ownedProject}`,command,expected,actual,verdict:'BLOCKED',artifacts:[evidencePath]})+'\n');
}
async function register(browser,lang='en'){
 const context=await browser.newContext({viewport:{width:375,height:812}}),page=await context.newPage();
 const email='operator-cabinet-'+randomUUID()+'@example.test',password='Local Operator cabinet '+randomUUID();
 step='register:open';
 await page.goto(origin+'/register?lang='+lang);
 step='register:submit';
 await page.getByLabel(lang==='en'?'Email':'Электронная почта',{exact:true}).fill(email);
 await page.getByRole('checkbox').nth(0).check();await page.getByRole('checkbox').nth(1).check();
 await page.getByRole('button',{name:lang==='en'?'Continue':'Продолжить',exact:true}).click();
 step='register:mail token';
 const token=await mailToken(email);
 step='register:verify';
 await page.goto(origin+'/verify-email?lang='+lang+'#token='+token);
 expect(new URL(page.url()).hash).toBe('');
 await page.getByLabel(lang==='en'?'Password':'Пароль',{exact:true}).fill(password);
 await page.getByRole('button',{name:lang==='en'?'Verify email':'Подтвердить email',exact:true}).click();
 await expect(page.getByText(lang==='en'?'Email verified. You can now sign in.':'Email подтверждён. Теперь войдите.')).toBeVisible();
 step='register:login';
 await page.goto(origin+'/login?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByLabel('Password',{exact:true}).fill(password);
 step='register:login submit';
 await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page).toHaveURL(/cabinet/);
 step='register:me';
 const me=await page.evaluate(async()=>await(await fetch('/api/v1/me')).json());
 return {context,page,id:me.account.account_id,email,password,csrf:me.csrf_token};
}
async function http(context,path,method='GET',body,csrf,key,headers={}){
 const cookie=(await context.cookies(origin)).find(item=>item.name==='__Host-session');
 const payload=body===undefined?null:Buffer.from(JSON.stringify(body));
 const requestHeaders={Cookie:cookie?'__Host-session='+cookie.value:'',...headers};
 if(method!=='GET'&&!Object.keys(requestHeaders).some(name=>name.toLowerCase()==='origin'))requestHeaders.Origin=origin;
 if(payload){requestHeaders['Content-Type']='application/json';requestHeaders['Content-Length']=String(payload.length);}
 if(csrf)requestHeaders['X-CSRF-Token']=csrf;
 if(key)requestHeaders['Idempotency-Key']=key;
 return new Promise((resolve,reject)=>{
  const request=https.request(origin+path,{method,ca:certPem,headers:requestHeaders,timeout:30000},response=>{
   const chunks=[];response.on('data',chunk=>chunks.push(chunk));response.on('end',()=>{
    try{const bytes=Buffer.concat(chunks),resultHeaders=response.headers,contentType=resultHeaders['content-type']??'';
     resolve({status:response.statusCode,headers:resultHeaders,
      body:contentType.includes('json')?JSON.parse(bytes.toString()):null,
      bytes:contentType.includes('octet-stream')?bytes:null});}catch(error){reject(error);}
   });
  });
  request.on('timeout',()=>request.destroy(new Error('HTTPS timeout')));
  request.on('error',reject);
  request.end(payload??undefined);
 });
}
const support=id=>'/api/v1/operator/clients/'+id+'/support';
const card=id=>origin+'/admin/clients/'+id+'/show?lang=en';
const validUUID=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
let browser,step='launch';
try{
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 const beforeVPN=bridge('vpn').connected;
 step='register three web actors';
 const operator=await register(browser),customer=await register(browser),other=await register(browser);
 check('Operator cabinet role setup','three distinct verified web identities, customer denied operator session',
  'three registered and logged in',new Set([operator.id,customer.id,other.id]).size===3);
 const denied=await http(customer.context,'/api/v1/operator/session');
 const foreign=await http(customer.context,'/api/v1/operator/clients/'+other.id);
 check('Operator cabinet direct role boundary','customer operator session/card refused',
  'HTTP '+denied.status+'/'+foreign.status,denied.status===403&&foreign.status===403);
 step='CLI operator grant';
 const granted=bridge('grant',{account:operator.id});
 check('Operator cabinet CLI grant','file-based grant applies to verified operator',
  'role present='+granted.role,granted.role===true&&bridge('vpn').connected===beforeVPN);
 const transport=bridge('transport');
 check('Operator cabinet web-only runtime','BOT_OPERATOR_IDS empty and Telegram bot adapter stopped',
  'no Telegram operators='+transport.no_telegram_operators+', adapter stopped='+transport.adapter_stopped,
  transport.no_telegram_operators&&transport.adapter_stopped);
 await operator.page.goto(origin+'/admin?lang=en');
 await expect(operator.page.getByRole('heading',{name:'Clients'})).toBeVisible();
 step='search and card';
 await operator.page.getByLabel('Search clients').fill(customer.email);
 await operator.page.getByRole('button',{name:'Search',exact:true}).click();
 await expect(operator.page.getByRole('link',{name:customer.email})).toBeVisible();
 await operator.page.goto(card(customer.id));
 await expect(operator.page.getByRole('heading',{name:customer.email})).toBeVisible();
 await expect(operator.page.getByText('Payment history is not available yet.')).toBeVisible();
 await expect(operator.page.getByText('Promocodes are not available yet.')).toBeVisible();
 check('Operator cabinet search/card','real web customer found, honest deferred fields, mobile fits',
  'card visible, no horizontal overflow',await operator.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 step='operator RU mobile keyboard search';
 await operator.page.goto(origin+'/admin?lang=ru');
 await expect(operator.page.getByRole('heading',{name:'Клиенты'})).toBeVisible();
 const ruSearch=operator.page.getByLabel('Поиск клиентов');
 await ruSearch.fill(customer.email);
 const ruSearchResponse=operator.page.waitForResponse(response=>response.url().endsWith('/api/v1/operator/clients/search')&&
  response.request().method()==='POST'&&response.request().postDataJSON()?.q===customer.email);
 await ruSearch.press('Enter');
 expect((await ruSearchResponse).status()).toBe(200);
 const ruCustomerLink=operator.page.getByRole('link',{name:customer.email});
 await expect(ruCustomerLink).toBeVisible();
 await ruCustomerLink.focus();await operator.page.keyboard.press('Enter');
 await expect(operator.page.getByRole('heading',{name:customer.email})).toBeVisible();
 check('Operator cabinet operator RU mobile keyboard search','Russian operator search and card open by Enter at 375px without horizontal overflow',
  'search HTTP 200, keyboard card visible',await operator.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 const knownWebActors=new Set([operator.id,customer.id,other.id]);
 const pageOne=await http(operator.context,'/api/v1/operator/clients/search','POST',{q:'operator-cabinet-',page:1,per_page:1},operator.csrf,undefined,{Origin:origin});
 const pageTwo=await http(operator.context,'/api/v1/operator/clients/search','POST',{q:'operator-cabinet-',page:2,per_page:1},operator.csrf,undefined,{Origin:origin});
 const firstPageId=pageOne.body?.clients?.[0]?.account_id,secondPageId=pageTwo.body?.clients?.[0]?.account_id;
 const pagingFacts={total_stable:pageOne.body?.total===pageTwo.body?.total,
  distinct:validUUID(firstPageId)&&validUUID(secondPageId)&&firstPageId!==secondPageId,
  own_actors:knownWebActors.has(firstPageId)&&knownWebActors.has(secondPageId)};
 check('Operator cabinet operator search API paging','per_page=1 returns two distinct own web actors on pages1/2 with stable total',
  'HTTP '+pageOne.status+'/'+pageTwo.status+', facts '+JSON.stringify(pagingFacts),
  pageOne.status===200&&pageTwo.status===200&&pageOne.body?.page===1&&pageTwo.body?.page===2&&
  pageOne.body?.per_page===1&&pageTwo.body?.per_page===1&&pageOne.body?.total>=3&&Object.values(pagingFacts).every(Boolean));
 await operator.page.goto(origin+'/admin?lang=en');
 step='support customer file';
 await customer.page.goto(origin+'/cabinet/support?lang=en');
 const file=Buffer.from([0,255,1,2,60,62,0,13]);
 await customer.page.getByLabel('Message').fill('Operator cabinet customer question');
 await customer.page.getByLabel('File').setInputFiles({name:'evidence.bin',mimeType:'application/octet-stream',buffer:file});
 const firstPost=customer.page.waitForResponse(response=>response.url().endsWith('/api/v1/support/messages')&&response.request().method()==='POST');
 await customer.page.getByRole('button',{name:'Send',exact:true}).click();
 expect((await firstPost).status()).toBe(201);
 await expect(customer.page.locator('.support-list').getByText('Operator cabinet customer question')).toBeVisible();
 const first=await http(customer.context,'/api/v1/support');
 const customerMessage=first.body?.messages?.find(m=>m.text==='Operator cabinet customer question');
 check('Support customer message','stored text and binary attachment with recipient unacknowledged',
  'HTTP '+first.status+', attachment '+(customerMessage?.attachment?'present':'absent'),
  first.status===200&&customerMessage?.delivery==='stored'&&customerMessage?.attachment?.size_bytes===file.length);
 const ownAck=await http(customer.context,'/api/v1/support/read','POST',{sequence:customerMessage.sequence},customer.csrf,undefined,{Origin:origin});
 check('Support recipient sequence boundary','sender cannot acknowledge own unreceived message',
  'HTTP '+ownAck.status,[400,409].includes(ownAck.status));
 const attachment='/api/v1/support/messages/'+customerMessage.id+'/attachment';
 const download=await http(customer.context,attachment);
 const foreignDownload=await http(other.context,attachment);
 const attachmentFacts={bytes_equal:download.bytes?.equals(file)===true,
  content_type:download.headers['content-type']==='application/octet-stream',
  disposition:download.headers['content-disposition']?.startsWith('attachment;')===true,
  nosniff:download.headers['x-content-type-options']==='nosniff',
  no_store:download.headers['cache-control']?.includes('no-store')===true};
 check('Support attachment boundary','exact binary bytes, download-only headers, foreign denied',
  'HTTP '+download.status+'/'+foreignDownload.status+', flags '+JSON.stringify(attachmentFacts),
  download.status===200&&download.bytes?.equals(file)&&download.headers['content-type']==='application/octet-stream'&&
  download.headers['content-disposition']?.startsWith('attachment;')&&download.headers['x-content-type-options']==='nosniff'&&
  download.headers['cache-control']?.includes('no-store')&&[403,404].includes(foreignDownload.status));
 await customer.page.goto(origin+'/cabinet?lang=en');
 step='support operator ack and reply';
 await operator.page.goto(card(customer.id));await expect(operator.page.getByText('Operator cabinet customer question')).toBeVisible();
 await expect.poll(async()=>(await http(operator.context,support(customer.id))).body?.conversation?.operator_received_sequence).toBe(customerMessage.sequence);
 await operator.page.getByLabel('Support reply').fill('Operator cabinet operator answer');
 await operator.page.getByLabel('Support file').setInputFiles({name:'reply.bin',mimeType:'application/octet-stream',buffer:file});
 const replyPost=operator.page.waitForResponse(response=>response.url().endsWith(support(customer.id)+'/messages')&&response.request().method()==='POST');
 await operator.page.getByRole('button',{name:'Send support reply'}).click();
 expect((await replyPost).status()).toBe(201);
 await expect(operator.page.locator('.support-list').getByText('Operator cabinet operator answer')).toBeVisible();
 const operatorView=await http(operator.context,support(customer.id));
 const reply=operatorView.body?.messages?.find(m=>m.text==='Operator cabinet operator answer');
 const replyDownload=await http(customer.context,'/api/v1/support/messages/'+reply.id+'/attachment');
 check('Support two-sided receipt','customer message delivered only after operator render ack; operator reply stored and attachment exact',
  'HTTP '+operatorView.status+'/'+replyDownload.status,
  operatorView.status===200&&operatorView.body.messages.find(m=>m.id===customerMessage.id)?.delivery==='delivered'&&
  reply?.delivery==='stored'&&replyDownload.bytes?.equals(file));
 await customer.page.goto(origin+'/cabinet/support?lang=en');await expect(customer.page.getByText('Operator cabinet operator answer')).toBeVisible();
 await expect.poll(async()=>(await http(customer.context,'/api/v1/support')).body?.conversation?.customer_received_sequence).toBe(reply.sequence);
 step='support duplicate and negative requests';
 const key=randomUUID(),payload={text:'Operator cabinet lost response fixture'};
 const once=await http(customer.context,'/api/v1/support/messages','POST',payload,customer.csrf,key,{Origin:origin});
 const twice=await http(customer.context,'/api/v1/support/messages','POST',payload,customer.csrf,key,{Origin:origin});
 const mismatch=await http(customer.context,'/api/v1/support/messages','POST',{text:'different'},customer.csrf,key,{Origin:origin});
 const badCSRF=await http(customer.context,'/api/v1/support/messages','POST',{text:'bad csrf'},'wrong',randomUUID(),{Origin:origin});
 const badOrigin=await http(customer.context,'/api/v1/support/messages','POST',{text:'bad origin'},customer.csrf,randomUUID(),{Origin:'https://foreign.example.test'});
 const tooLong=await http(customer.context,'/api/v1/support/messages','POST',{text:'x'.repeat(4001)},customer.csrf,randomUUID(),{Origin:origin});
 const nul=await http(customer.context,'/api/v1/support/messages','POST',{text:'x\u0000y'},customer.csrf,randomUUID(),{Origin:origin});
 const afterDuplicate=bridge('support',{account:customer.id});
 check('Support idempotency/input/CSRF/Origin','one durable retry; mismatch409; invalid text, CSRF and Origin denied',
  'HTTP '+[once.status,twice.status,mismatch.status,badCSRF.status,badOrigin.status,tooLong.status,nul.status].join('/')+', stored count '+afterDuplicate.messages,
  once.status===201&&[200,201].includes(twice.status)&&twice.body?.id===once.body?.id&&mismatch.status===409&&
  [400,403].includes(badCSRF.status)&&[400,403].includes(badOrigin.status)&&tooLong.status===400&&nul.status===400&&
  afterDuplicate.messages===3);
 step='support close/reopen/ban';
 await operator.page.getByRole('button',{name:'Close conversation'}).click();
 await expect(operator.page.getByText('Conversation closed.')).toBeVisible();
 await customer.page.reload();await expect(customer.page.getByRole('button',{name:'Reopen conversation'})).toBeVisible();
 await customer.page.getByLabel('Message').fill('Operator cabinet auto reopened');
 await customer.page.getByRole('button',{name:'Reopen and send'}).click();
 await expect(customer.page.getByText('Operator cabinet auto reopened')).toBeVisible();
 await operator.page.reload();await operator.page.getByLabel('Support restriction reason').fill('Operator cabinet local limit');
 await operator.page.getByRole('button',{name:'Block support'}).click();
 await customer.page.reload();await expect(customer.page.getByText('Support has blocked new messages.')).toBeVisible();
 const stillHistory=await http(customer.context,'/api/v1/support');
 const blockedSend=await http(customer.context,'/api/v1/support/messages','POST',{text:'blocked'},customer.csrf,randomUUID(),{Origin:origin});
 check('Support support ban','history retained, customer write blocked, same VPN connectivity',
  'HTTP '+stillHistory.status+'/'+blockedSend.status,stillHistory.status===200&&blockedSend.status===403&&bridge('vpn').connected===beforeVPN);
 await operator.page.getByLabel('Support restriction reason').fill('Operator cabinet local restored');
 await operator.page.getByRole('button',{name:'Unblock support'}).click();
 step='second customer and RU mobile support';
 await other.page.goto(origin+'/cabinet/support?lang=ru');
 await other.page.getByLabel('Сообщение').fill('Operator cabinet второй клиент');
 const secondPost=other.page.waitForResponse(response=>response.url().endsWith('/api/v1/support/messages')&&response.request().method()==='POST');
 await other.page.getByRole('button',{name:'Отправить'}).focus();await other.page.keyboard.press('Enter');
 expect((await secondPost).status()).toBe(201);
 await expect(other.page.locator('.support-list').getByText('Operator cabinet второй клиент')).toBeVisible();
 await operator.page.goto(card(other.id));
 await expect(operator.page.getByText('Operator cabinet второй клиент')).toBeVisible();
 await operator.page.getByLabel('Support reply').fill('Operator cabinet second answer');
 await operator.page.getByRole('button',{name:'Send support reply'}).click();
 await other.page.reload();await expect(other.page.getByText('Operator cabinet second answer')).toBeVisible();
 check('Support second customer ru/mobile/keyboard','distinct support conversation, RU customer controls, Enter send, mobile width',
  'second conversation visible',await other.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 step='support page 50';
 let pageWrites=true;
 for(let index=0;index<25;index++){
  const customerWrite=await http(other.context,'/api/v1/support/messages','POST',{text:'Operator cabinet history customer '+index},other.csrf,randomUUID(),{Origin:origin});
  const operatorWrite=await http(operator.context,support(other.id)+'/messages','POST',{text:'Operator cabinet history operator '+index},operator.csrf,randomUUID(),{Origin:origin});
  pageWrites&&=customerWrite.status===201&&operatorWrite.status===201;
  if(!pageWrites)break;
 }
 const latest=await http(other.context,'/api/v1/support');
 const previous=latest.body?.oldest_sequence===null?null:await http(other.context,'/api/v1/support/history','POST',
  {before_sequence:latest.body.oldest_sequence},other.csrf,undefined,{Origin:origin});
 await other.page.reload();
 await expect(other.page.getByRole('button',{name:'Загрузить предыдущие сообщения'})).toBeVisible();
 await other.page.getByRole('button',{name:'Загрузить предыдущие сообщения'}).click();
 await expect(other.page.getByText('Operator cabinet второй клиент')).toBeVisible();
 check('Support history page boundary','52 actual cross-actor messages, latest50 and older page reachable by UI',
  'writes '+(pageWrites?'complete':'incomplete')+', latest '+latest.body?.messages?.length+', older '+previous?.body?.messages?.length,
  pageWrites&&latest.status===200&&latest.body?.messages?.length===50&&latest.body?.has_more===true&&
  previous?.status===200&&previous.body?.messages?.length>=2);
 step='web trial decisions';
 await customer.page.goto(origin+'/cabinet?lang=en');
 const customerTrialPost=customer.page.waitForResponse(response=>response.url().endsWith('/api/v1/trial-requests')&&response.request().method()==='POST');
 await customer.page.getByRole('button',{name:'Request trial'}).click();
 expect((await customerTrialPost).status()).toBe(201);
 const current=await http(customer.context,'/api/v1/trial-requests/current');
 const requestId=current.body?.request?.request_id;
 check('Operator cabinet current trial identity','completed customer POST has a real UUID before any operator decision',
  'current HTTP '+current.status+', UUID valid='+validUUID(requestId),current.status===200&&validUUID(requestId));
 const forged=await http(operator.context,'/api/v1/operator/trial-requests/'+requestId+'/decision','POST',
  {decision:'approve',reason:'',operator_account_id:other.id},operator.csrf,randomUUID(),{Origin:origin});
 check('Operator cabinet forged web actor','extra actor body rejected before decision',
  'HTTP '+forged.status,forged.status===400);
 await operator.page.goto(card(customer.id));
 const pending=operator.page.locator('.admin-action').filter({hasText:'pending'}).first();
 await pending.getByLabel('Reject reason').fill('Operator cabinet initial reason');
 const rejectionPost=operator.page.waitForResponse(response=>response.url().endsWith('/decision')&&response.request().method()==='POST');
 await pending.getByRole('button',{name:'Reject'}).click();
 expect((await rejectionPost).status()).toBe(200);
 await expect(operator.page.getByText('rejected:')).toBeVisible();
 const rejected=await http(operator.context,'/api/v1/operator/clients/'+customer.id);
 const rejectRow=rejected.body?.trial_requests?.find(r=>r.request_id===requestId);
 const rejectFacts={matched:!!rejectRow,status_rejected:rejectRow?.status==='rejected',
  actor_matches:rejectRow?.operator_account_id===operator.id,
  telegram_field_present:!!rejectRow&&Object.hasOwn(rejectRow,'operator_tg_id'),
  telegram_actor_null:rejectRow?.operator_tg_id===null};
 check('Operator cabinet web actor reject','rejected request has UUID web actor, no fabricated Telegram actor',
  'HTTP '+rejected.status+', facts '+JSON.stringify(rejectFacts),
  rejected.status===200&&Object.values(rejectFacts).every(Boolean));
 const immutable=await http(operator.context,'/api/v1/operator/trial-requests/'+requestId+'/decision','POST',
  {decision:'approve',reason:''},operator.csrf,randomUUID(),{Origin:origin});
 check('Operator cabinet immutable rejection','separate approve cannot mutate rejected request',
  'HTTP '+immutable.status,immutable.status===409);
 await operator.page.reload();const row=operator.page.locator('.admin-action').filter({hasText:'rejected'}).first();
 await row.getByLabel('Reconsider reason').fill('Operator cabinet new evidence');
 const reconsiderPost=operator.page.waitForResponse(response=>response.url().endsWith('/reconsider')&&response.request().method()==='POST');
 await row.getByRole('button',{name:'Reconsider'}).click();
 expect((await reconsiderPost).status()).toBe(201);
 const reconsidered=await http(operator.context,'/api/v1/operator/clients/'+customer.id);
 const reconsideredRow=reconsidered.body?.trial_requests?.find(item=>item.status==='pending'&&item.previous_request_id===requestId);
 check('Operator cabinet reconsidered request identity','completed reconsider POST creates a distinct pending UUID linked to the rejected request',
  'card HTTP '+reconsidered.status+', UUID valid='+validUUID(reconsideredRow?.request_id),
  reconsidered.status===200&&validUUID(reconsideredRow?.request_id)&&reconsideredRow.request_id!==requestId);
 await operator.page.reload();const newPending=operator.page.locator('.admin-action').filter({hasText:'pending'}).first();
 const approvalRequest=operator.page.waitForRequest(request=>request.url().endsWith('/decision')&&request.method()==='POST');
 const approvalResponse=operator.page.waitForResponse(response=>response.url().endsWith('/decision')&&response.request().method()==='POST');
 await newPending.getByRole('button',{name:'Approve'}).click();
 const sentApproval=await approvalRequest;
 expect((await approvalResponse).status()).toBe(200);
 await expect.poll(async()=>(await http(customer.context,'/api/v1/subscription')).body?.status,{timeout:30000}).toBe('active');
 const approved=await http(operator.context,'/api/v1/operator/clients/'+customer.id);
 const approvedRow=approved.body?.trial_requests?.find(r=>r.request_id===reconsideredRow.request_id);
 if(approved.status!==200||approvedRow?.status!=='approved'||!validUUID(approvedRow.operation_id))throw new Error('approved operation identity absent');
 const replay=await http(operator.context,new URL(sentApproval.url()).pathname,'POST',
  {decision:'approve',reason:''},operator.csrf,sentApproval.headers()['idempotency-key'],{Origin:origin});
 const native=bridge('native',{operation:approvedRow.operation_id});
 check('Operator cabinet web-only approval/native','UUID actor, one Grant/job, native target identity after shared worker',
  'operation '+native.status+', replay HTTP '+replay.status+', grants '+native.grants+', jobs '+native.jobs,
  approvedRow.operator_account_id===operator.id&&approvedRow.operator_tg_id===null&&
  [200,201].includes(replay.status)&&replay.body?.operation_id===approvedRow.operation_id&&
  native.status==='applied'&&native.grants===1&&native.jobs===1&&native.panel_identity);
 const keyBefore=await http(customer.context,'/api/v1/subscription/key');
 await operator.page.goto(card(customer.id));
 await operator.page.getByLabel('Support restriction reason').fill('Operator cabinet active trial remains intact');
 await operator.page.getByRole('button',{name:'Block support'}).click();
 const keyAfter=await http(customer.context,'/api/v1/subscription/key');
 const activeDuringBan=await http(customer.context,'/api/v1/subscription');
 const deniedDuringBan=await http(customer.context,'/api/v1/support/messages','POST',{text:'denied while active'},customer.csrf,randomUUID(),{Origin:origin});
 check('Support support ban independent from VPN','customer support write denied while active trial/key and own Docker proxy remain',
  'key HTTP '+keyBefore.status+'/'+keyAfter.status+', support HTTP '+deniedDuringBan.status+', subscription '+activeDuringBan.body?.status,
  keyBefore.status===200&&keyAfter.status===200&&keyBefore.body?.subscription_url===keyAfter.body?.subscription_url&&
  deniedDuringBan.status===403&&activeDuringBan.body?.status==='active'&&bridge('vpn').connected===beforeVPN);
 await operator.page.getByLabel('Support restriction reason').fill('Operator cabinet support restored');
 await operator.page.getByRole('button',{name:'Unblock support'}).click();
 step='controlled needs_review';
 let reviewOperation,reviewNativeBefore,reviewNativeAfter,reviewInstalled=false,reviewReady=false;
 try{
  reviewInstalled=bridge('review_on',{account:other.id}).installed;
  await other.page.goto(origin+'/cabinet?lang=en');
  const reviewTrialPost=other.page.waitForResponse(response=>response.url().endsWith('/api/v1/trial-requests')&&response.request().method()==='POST');
  await other.page.getByRole('button',{name:'Request trial'}).click();
  expect((await reviewTrialPost).status()).toBe(201);
  const reviewCurrent=await http(other.context,'/api/v1/trial-requests/current');
  if(reviewCurrent.status!==200||!validUUID(reviewCurrent.body?.request?.request_id))throw new Error('review request identity absent');
  await operator.page.goto(card(other.id));
  const reviewPending=operator.page.locator('.admin-action').filter({hasText:'pending'}).first();
  const reviewDecisionPost=operator.page.waitForResponse(response=>response.url().endsWith('/decision')&&response.request().method()==='POST');
  await reviewPending.getByRole('button',{name:'Approve'}).click();
  expect((await reviewDecisionPost).status()).toBe(200);
  for(let attempt=0;attempt<60;attempt++){
   const reviewCard=await http(operator.context,'/api/v1/operator/clients/'+other.id);
   reviewOperation=reviewCard.body?.trial_requests?.find(row=>row.status==='approved')?.operation_id;
   if(reviewOperation)break;
   await new Promise(resolve=>setTimeout(resolve,200));
  }
  if(!validUUID(reviewOperation))throw new Error('review operation absent');
  for(let attempt=0;attempt<120;attempt++){
   reviewNativeBefore=bridge('native',{operation:reviewOperation});
   if(reviewNativeBefore.status==='needs_review')break;
   await new Promise(resolve=>setTimeout(resolve,500));
  }
  reviewReady=reviewNativeBefore?.status==='needs_review'&&reviewNativeBefore?.grants===1&&reviewNativeBefore?.panel_identity;
 }catch{reviewReady=false;
 }finally{if(reviewInstalled)bridge('review_off');}
 if(reviewReady){
  step='web UUID reconcile';
  await operator.page.goto(card(other.id));
  await operator.page.getByLabel('Reconcile reason').fill('Operator cabinet controlled recovery');
  const reconcilePost=operator.page.waitForResponse(response=>response.url().endsWith('/reconcile')&&response.request().method()==='POST');
  await operator.page.getByRole('button',{name:'Reconcile'}).click();
  expect((await reconcilePost).status()).toBe(202);
  for(let attempt=0;attempt<120;attempt++){
   reviewNativeAfter=bridge('native',{operation:reviewOperation});
   if(reviewNativeAfter.status==='applied')break;
   await new Promise(resolve=>setTimeout(resolve,500));
  }
  const afterReviewCard=await http(operator.context,'/api/v1/operator/clients/'+other.id);
  const webAudit=afterReviewCard.body?.audit_events?.some(event=>event.action==='provision_reconcile_requested'&&event.operator_account_id===operator.id);
  check('Operator cabinet controlled web reconciliation','real needs_review through scoped apply failure; web actor requeues one Grant with same native target',
   'before '+reviewNativeBefore.status+', after '+reviewNativeAfter?.status+', grants '+reviewNativeAfter?.grants,
   reviewNativeAfter?.status==='applied'&&reviewNativeAfter.grants===1&&reviewNativeAfter.panel_identity&&webAudit&&
   reviewNativeAfter.target_digest===reviewNativeBefore.target_digest&&reviewNativeAfter.panel_digest===reviewNativeBefore.panel_digest);
 }else blocked('Operator cabinet controlled web reconciliation','guarded own needs_review and web actor requeue',
  'guard or operation did not produce safe needs_review; fixture trigger removed, no PASS');
 step='Telegram-only genuine owner identity';
 let tgClientId,tgOperation,tgNativeBefore;
 let available=false,tgPrerequisite='owner ID already present; creation not attempted';
 try{available=bridge('tg_available').available;}catch{tgPrerequisite='owner ID unavailable; creation not attempted';}
 if(available){
  const tgId=genuineTelegramId();
  await operator.page.goto(origin+'/admin?lang=en');await operator.page.getByLabel('Telegram ID').fill(tgId);
  await operator.page.getByLabel('Display name').fill('Operator cabinet local owner');
  const telegramTrialPost=operator.page.waitForResponse(response=>response.url().endsWith('/api/v1/operator/clients/trial')&&response.request().method()==='POST');
  await operator.page.getByRole('button',{name:'Create Telegram trial'}).click();
  expect((await telegramTrialPost).status()).toBe(201);
  await expect(operator.page.getByText('Telegram client and trial created.')).toBeVisible();
  const search=await http(operator.context,'/api/v1/operator/clients/search','POST',{q:tgId,page:1,per_page:50},operator.csrf,undefined,{Origin:origin});
  const client=search.body?.clients?.find(row=>row.telegram_id===tgId);
  if(!validUUID(client?.account_id))throw new Error('Telegram-only account identity absent');
  tgClientId=client?.account_id;
  const tgCard=await http(operator.context,'/api/v1/operator/clients/'+client.account_id);
  const tgRow=tgCard.body?.trial_requests?.find(row=>row.status==='approved');
  if(!validUUID(tgRow?.request_id)||!validUUID(tgRow?.operation_id))throw new Error('Telegram-only operation identity absent');
  tgOperation=tgRow?.operation_id;
  let tgNative;
  for(let attempt=0;attempt<120;attempt++){
   tgNative=bridge('native',{operation:tgRow.operation_id});
   if(tgNative.status==='applied')break;
   await new Promise(resolve=>setTimeout(resolve,500));
  }
  tgNativeBefore=tgNative;
  const tgIdentity=bridge('identity',{account:client.account_id});
  const duplicateTG=await http(operator.context,'/api/v1/operator/clients/trial','POST',
   {telegram_id:tgId,display_name:'Operator cabinet local owner',locale:'ru'},operator.csrf,randomUUID(),{Origin:origin});
  check('Operator cabinet genuine Telegram-only identity','true owner ID remains decimal string, NULL email and credentials, one native grant',
   'HTTP '+search.status+'/'+tgCard.status+', duplicate '+duplicateTG.status+', grants '+tgNative.grants,
   client.kind==='telegram'&&client.email===null&&typeof client.telegram_id==='string'&&
   tgIdentity.email_null&&tgIdentity.password_null&&tgIdentity.verified_null&&tgIdentity.telegram_present&&
   duplicateTG.status===409&&tgNative.status==='applied'&&tgNative.grants===1&&tgNative.panel_identity);
 }else blocked('Operator cabinet genuine Telegram-only prerequisite','valid unused owner ID in worktree .env and new local PG',tgPrerequisite);
 step='PostgreSQL dump/restore of new Support/Operator cabinet data';
 // Keep every owned browser off polling/receipt/profile surfaces while both
 // snapshots and the real dump/restore run.
 await Promise.all([operator.page,customer.page,other.page].map(page=>page.goto(origin+'/login?lang=en')));
 const beforeSupport=bridge('support',{account:customer.id});
 const beforeOperator=bridge('identity',{account:operator.id});
 const beforeCustomer=bridge('identity',{account:customer.id});
 const beforeTelegram=tgClientId?bridge('identity',{account:tgClientId}):null;
 const beforeDigests=[operator.id,customer.id,other.id,...(tgClientId?[tgClientId]:[])].map(account=>bridge('digest',{account}));
 const restored=bridge('restore');
 const afterSupport=bridge('support',{account:customer.id});
 const afterOperator=bridge('identity',{account:operator.id});
 const afterCustomer=bridge('identity',{account:customer.id});
 const afterTelegram=tgClientId?bridge('identity',{account:tgClientId}):null;
 const afterDigests=[operator.id,customer.id,other.id,...(tgClientId?[tgClientId]:[])].map(account=>bridge('digest',{account}));
 const nativeAfterRestore=bridge('native',{operation:approvedRow.operation_id});
 const telegramNativeAfterRestore=tgOperation?bridge('native',{operation:tgOperation}):null;
 const reviewNativeAfterRestore=reviewReady?bridge('native',{operation:reviewOperation}):null;
 const oldSession=await http(operator.context,'/api/v1/operator/session');
 check('Support/Operator cabinet actual PG restore','support text/bytea/read state and web role/actor/grant retained; old session revoked; prior Docker VPN restored',
  'snapshot equality '+(JSON.stringify(beforeSupport)===JSON.stringify(afterSupport))+'/'+
   (JSON.stringify(beforeOperator)===JSON.stringify(afterOperator))+'/'+
   (JSON.stringify(beforeCustomer)===JSON.stringify(afterCustomer))+'/'+
   (JSON.stringify(beforeTelegram)===JSON.stringify(afterTelegram))+'/'+
   (JSON.stringify(beforeDigests)===JSON.stringify(afterDigests))+', old HTTP '+oldSession.status,
  restored.restored&&restored.prior_vpn_connected&&restored.prior_vpn_config_equal&&
  restored.no_telegram_operators&&restored.adapter_stopped&&oldSession.status===401&&
  JSON.stringify(beforeSupport)===JSON.stringify(afterSupport)&&
  JSON.stringify(beforeOperator)===JSON.stringify(afterOperator)&&
  JSON.stringify(beforeCustomer)===JSON.stringify(afterCustomer)&&
  JSON.stringify(beforeTelegram)===JSON.stringify(afterTelegram)&&
  JSON.stringify(beforeDigests)===JSON.stringify(afterDigests)&&
  nativeAfterRestore.status==='applied'&&nativeAfterRestore.grants===1&&nativeAfterRestore.panel_identity&&
  nativeAfterRestore.target_digest===native.target_digest&&nativeAfterRestore.panel_digest===native.panel_digest&&
  (!reviewNativeAfterRestore||reviewNativeAfterRestore.status==='applied'&&reviewNativeAfterRestore.grants===1&&
   reviewNativeAfterRestore.target_digest===reviewNativeAfter.target_digest&&reviewNativeAfterRestore.panel_digest===reviewNativeAfter.panel_digest)&&
  (!telegramNativeAfterRestore||telegramNativeAfterRestore.status==='applied'&&telegramNativeAfterRestore.grants===1&&telegramNativeAfterRestore.panel_identity&&
   telegramNativeAfterRestore.target_digest===tgNativeBefore.target_digest&&telegramNativeAfterRestore.panel_digest===tgNativeBefore.panel_digest));
 await operator.page.goto(origin+'/login?lang=en');
 await operator.page.getByLabel('Email',{exact:true}).fill(operator.email);
 await operator.page.getByLabel('Password',{exact:true}).fill(operator.password);
 await operator.page.getByRole('button',{name:'Sign in',exact:true}).click();
 await expect(operator.page).toHaveURL(/cabinet/);
 const fresh=await http(operator.context,'/api/v1/operator/session');
 await customer.page.goto(origin+'/login?lang=en');
 await customer.page.getByLabel('Email',{exact:true}).fill(customer.email);
 await customer.page.getByLabel('Password',{exact:true}).fill(customer.password);
 await customer.page.getByRole('button',{name:'Sign in',exact:true}).click();
 await expect(customer.page).toHaveURL(/cabinet/);
 const restoredFile=await http(customer.context,attachment);
 const restoredKey=await http(customer.context,'/api/v1/subscription/key');
 check('Operator cabinet restored fresh login','verified operator can log in fresh and role survives',
  'HTTP '+fresh.status+'/'+restoredFile.status+'/'+restoredKey.status,
  fresh.status===200&&restoredFile.status===200&&restoredFile.bytes?.equals(file)&&
  restoredKey.status===200&&restoredKey.body?.subscription_url===keyBefore.body.subscription_url);
 step='restricted operator sensitive clear';
 await operator.page.goto(card(customer.id));
 await operator.page.getByRole('button',{name:'Show subscription link'}).click();
 await expect(operator.page.getByLabel('Subscription link')).toBeVisible();
 const originalRestriction=bridge('restricted_get',{account:operator.id});
 let restrictedRead,restrictedCard,restrictedCleared=false;
 try{
  bridge('restricted_set',{account:operator.id,restricted:true});
  restrictedRead=await http(operator.context,'/api/v1/operator/session');
  restrictedCard=await http(operator.context,'/api/v1/operator/clients/'+customer.id);
  await operator.page.getByRole('button',{name:'Close conversation'}).click();
  await expect(operator.page.getByRole('heading',{name:'Operator access required'})).toBeVisible();
  restrictedCleared=await operator.page.getByLabel('Subscription link').count()===0&&
   !String(await operator.page.evaluate(()=>JSON.stringify([localStorage,sessionStorage]))).includes(keyBefore.body.subscription_url);
 }finally{
  bridge('restricted_set',{account:operator.id,restricted:originalRestriction.restricted});
 }
 const unrestrictedRead=await http(operator.context,'/api/v1/operator/session');
 check('Operator cabinet restricted operator boundary','dedicated operator flag denies protected reads and clears shown key; exact prior flag restored with role intact',
  'HTTP '+restrictedRead?.status+'/'+restrictedCard?.status+'/'+unrestrictedRead.status+', key cleared='+restrictedCleared,
  originalRestriction.operator_role&&originalRestriction.restricted===false&&restrictedRead?.status===403&&restrictedCard?.status===403&&
  restrictedCleared&&unrestrictedRead.status===200&&bridge('vpn').connected===beforeVPN);
 step='CLI revoke and sensitive clear';
 await operator.page.goto(card(customer.id));await operator.page.getByRole('button',{name:'Show subscription link'}).click();
 await expect(operator.page.getByLabel('Subscription link')).toBeVisible();
 const revoked=bridge('revoke',{account:operator.id});
 const directAfter=await http(operator.context,'/api/v1/operator/clients/'+customer.id);
 await operator.page.getByRole('button',{name:'Close conversation'}).click();
 await expect(operator.page.getByRole('heading',{name:'Operator access required'})).toBeVisible();
 check('Operator cabinet revoked role','direct HTTP denied and sensitive key removed from visible UI',
  'role present='+revoked.role+', HTTP '+directAfter.status,
  revoked.role===false&&directAfter.status===403&&await operator.page.getByLabel('Subscription link').count()===0&&
  !String(await operator.page.evaluate(()=>JSON.stringify([localStorage,sessionStorage]))).includes(keyBefore.body.subscription_url));
 await operator.page.getByRole('button',{name:'Sign out'}).click();
 await expect(operator.page).toHaveURL(/login/);
 check('Operator cabinet revoked logout','denied operator can sign out and session is gone',
  'HTTP '+(await http(operator.context,'/api/v1/me')).status,(await http(operator.context,'/api/v1/me')).status===401);
 check('Support/Operator cabinet final VPN','pre-existing own Docker proxy connection preserved',
  'proxy connected='+bridge('vpn').connected,beforeVPN&&bridge('vpn').connected);
 console.log('Support/Operator cabinet native browser completed; redacted evidence: '+evidencePath);
}catch{
 writeSync(evidenceFd,JSON.stringify({criterion:'harness execution',target:`${ownedProject}`,command,expected:'all scheduled checks execute',actual:'stopped at '+step,verdict:'FAIL',artifacts:[evidencePath]})+'\n');
 console.error('FAIL: Support/Operator cabinet native browser step '+step+'; private redacted evidence: '+evidencePath);
 process.exitCode=1;
}finally{await browser?.close();closeSync(evidenceFd);}
