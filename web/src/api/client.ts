import type {components} from './schema.gen';
export type RegisterInput=components["schemas"]["RegisterInput"];
export type RegistrationAccepted=components["schemas"]["RegistrationAccepted"];
export type VerifyInput=components["schemas"]["VerifyInput"];
export type VerifyResult=components["schemas"]["VerifyResult"];
export type ResendInput=components["schemas"]["ResendInput"];
export type ResendAccepted=components["schemas"]["ResendAccepted"];
export type LoginInput=components["schemas"]["LoginInput"];
export type LoginResult=components["schemas"]["LoginResult"];
export type AccountResult=components["schemas"]["AccountResult"];
export type TrialRequestInput=components["schemas"]["TrialRequestInput"];
export type TrialRequest=components["schemas"]["TrialRequest"];
export type CurrentTrialRequest=components["schemas"]["CurrentTrialRequest"];
export type Subscription=components["schemas"]["Subscription"];
export type SubscriptionKey=components["schemas"]["SubscriptionKey"];

export class ApiError extends Error {
 constructor(public status:number,public code:string,public request_id:string,public retryAfter=0){super(code);}
}
let csrf:string|undefined;
async function request<T>(path:string,method='GET',body?:unknown,signal?:AbortSignal,sessionWrite=false,key?:string):Promise<T>{
 const form=body instanceof FormData;const headers:Record<string,string>={};if(body!==undefined&&!form)headers['Content-Type']='application/json';if(sessionWrite&&csrf)headers['X-CSRF-Token']=csrf;if(key)headers['Idempotency-Key']=key;
 let response:Response;
 try{response=await fetch('/api/v1/'+path,{method,body:body===undefined?undefined:form?body:JSON.stringify(body),headers,credentials:'same-origin',cache:'no-store',signal:signal?AbortSignal.any([signal,AbortSignal.timeout(15000)]):AbortSignal.timeout(15000)});}catch(error){if(signal?.aborted)throw error;throw new ApiError(503,'SERVICE_UNAVAILABLE','');}
 signal?.throwIfAborted();
 if(!response.ok){if(response.status===401)csrf=undefined;let failure:unknown;try{failure=await response.json();}catch{failure={};}const raw=failure as Partial<components['schemas']['APIError']>;const id=raw.error?.request_id??'';const delay=Number(response.headers.get('Retry-After'));throw new ApiError(response.status,raw.error?.code??'SERVICE_UNAVAILABLE',/^[0-9a-f-]{36}$/i.test(id)?id:'',Number.isFinite(delay)&&delay>0?Math.min(86400,Math.ceil(delay)):0);}
 if(response.status===204)return undefined as T;
 try{const out=await response.json() as T;signal?.throwIfAborted();return out;}catch{if(signal?.aborted)throw signal.reason;throw new ApiError(503,'SERVICE_UNAVAILABLE','');}
}
export const registerAccount=(input:RegisterInput)=>request<RegistrationAccepted>('auth/register','POST',input);
export const verifyEmail=(input:VerifyInput)=>request<VerifyResult>('auth/verify-email','POST',input);
export const resendVerification=(input:ResendInput)=>request<ResendAccepted>('auth/resend-verification','POST',input);
export async function loginAccount(input:LoginInput){const out=await request<LoginResult>('auth/login','POST',input);csrf=out.csrf_token;return out;}
export async function logoutAccount(){try{await request<void>('auth/logout','POST',undefined,undefined,true);}finally{csrf=undefined;}}
export async function getAccount(signal?:AbortSignal){const out=await request<AccountResult>('me','GET',undefined,signal);csrf=out.csrf_token;return out;}
export const createTrialRequest=(input:TrialRequestInput,key:string,signal?:AbortSignal)=>request<TrialRequest>('trial-requests','POST',input,signal,true,key);
export const getCurrentTrialRequest=(signal?:AbortSignal)=>request<CurrentTrialRequest>('trial-requests/current','GET',undefined,signal);
export const getSubscription=(signal?:AbortSignal)=>request<Subscription>('subscription','GET',undefined,signal);
export const getSubscriptionKey=(signal?:AbortSignal)=>request<SubscriptionKey>('subscription/key','GET',undefined,signal);

export type PasswordResetInput=components['schemas']['PasswordResetInput'];
export type PasswordResetAccepted=components['schemas']['PasswordResetAccepted'];
export type PasswordResetCompleteInput=components['schemas']['PasswordResetCompleteInput'];
export const requestPasswordReset=(input:PasswordResetInput)=>request<PasswordResetAccepted>('auth/password-reset','POST',input);
export const completePasswordReset=(input:PasswordResetCompleteInput)=>request<void>('auth/password-reset/complete','POST',input);

export type PasswordChangeInput=components['schemas']['PasswordChangeInput'];
export type CurrentPasswordInput=components['schemas']['CurrentPasswordInput'];
export type SessionContext=components['schemas']['SessionContext'];
export type AccountSecurity=components['schemas']['AccountSecurity'];
export const clearSession=()=>{csrf=undefined;};
export async function getSessionContext(signal?:AbortSignal){const out=await request<SessionContext>('auth/session','GET',undefined,signal);csrf=out.csrf_token;return out;}
export const getAccountSecurity=(signal?:AbortSignal)=>request<AccountSecurity>('me/security','GET',undefined,signal);
export const changePassword=(input:PasswordChangeInput)=>request<void>('me/password-change','POST',input,undefined,true);
export const revokeOtherSessions=(input:CurrentPasswordInput)=>request<void>('me/sessions/revoke-others','POST',input,undefined,true);

export type EmailChangeInput=components['schemas']['EmailChangeInput'];
export type EmailChangeAccepted=components['schemas']['EmailChangeAccepted'];
export type EmailChangeConfirmInput=components['schemas']['EmailChangeConfirmInput'];
export type EmailChangeResult=components['schemas']['EmailChangeResult'];
export const requestEmailChange=(input:EmailChangeInput)=>request<EmailChangeAccepted>('me/email-change','POST',input,undefined,true);
export const confirmEmailChange=(input:EmailChangeConfirmInput)=>request<EmailChangeResult>('auth/email-change/confirm','POST',input);
export const cancelEmailChange=()=>request<void>('me/email-change/cancel','POST',undefined,undefined,true);

export type SupportAttachment=components['schemas']['SupportAttachment'];
export type SupportMessage=components['schemas']['SupportMessage'];
export type SupportConversation=components['schemas']['SupportConversation'];
export type SupportResult=components['schemas']['SupportResult'];
export const getSupport=(signal?:AbortSignal)=>request<SupportResult>('support','GET',undefined,signal);
export const getSupportHistory=(before_sequence:number,signal?:AbortSignal)=>request<SupportResult>('support/history','POST',{before_sequence},signal,true);
export function createSupportMessage(text:string,file:File|undefined,key:string,signal?:AbortSignal){if(!file)return request<SupportMessage>('support/messages','POST',{text},signal,true,key);const body=new FormData();body.append('text',text);body.append('file',file);return request<SupportMessage>('support/messages','POST',body,signal,true,key);}
export const acknowledgeSupport=(sequence:number,signal?:AbortSignal)=>request<void>('support/read','POST',{sequence},signal,true);
export const setSupportState=(status:'open'|'closed',signal?:AbortSignal)=>request<void>('support/state','POST',{status},signal,true);
export const supportAttachmentURL=(id:string)=>'/api/v1/support/messages/'+encodeURIComponent(id)+'/attachment';

export type OperatorSession=components['schemas']['OperatorSession'];
export type OperatorClient=components['schemas']['OperatorClient'];
export type OperatorSearchResult=components['schemas']['OperatorSearchResult'];
export type OperatorClientCard=components['schemas']['OperatorClientCard'];
export type OperatorTrialRequest=components['schemas']['OperatorTrialRequest'];
export type OperatorAuditEvent=components['schemas']['OperatorAuditEvent'];
export type OperatorHistoryResult=components['schemas']['OperatorHistoryResult'];
export type OperatorDecisionResult=components['schemas']['OperatorDecisionResult'];
export type OperatorTelegramTrialResult=components['schemas']['OperatorTelegramTrialResult'];
export type OperatorLegacyApprovalEvent=components['schemas']['OperatorLegacyApprovalEvent'];
export type OperatorRestrictionResult=components['schemas']['OperatorRestrictionResult'];
const operatorClientPath=(id:string)=>'operator/clients/'+encodeURIComponent(id);
export async function getOperatorSession(signal?:AbortSignal){const out=await request<OperatorSession>('operator/session','GET',undefined,signal);csrf=out.csrf_token;return out;}
export const searchOperatorClients=(q:string,page:number,signal?:AbortSignal)=>request<OperatorSearchResult>('operator/clients/search','POST',{q,page,per_page:50},signal,true);
export const getOperatorClient=(id:string,signal?:AbortSignal)=>request<OperatorClientCard>(operatorClientPath(id),'GET',undefined,signal);
export const getOperatorClientHistory=(id:string,input:components['schemas']['OperatorHistoryInput'],signal?:AbortSignal)=>request<OperatorHistoryResult>(operatorClientPath(id)+'/history','POST',input,signal,true);
export const setOperatorRestriction=(id:string,input:components['schemas']['OperatorRestrictionInput'],key:string,signal?:AbortSignal)=>request<OperatorRestrictionResult>(operatorClientPath(id)+'/restriction','POST',input,signal,true,key);
export const getOperatorClientKey=(id:string,signal?:AbortSignal)=>request<SubscriptionKey>(operatorClientPath(id)+'/key','GET',undefined,signal);
export const decideOperatorTrial=(id:string,input:components['schemas']['OperatorDecisionInput'],key:string,signal?:AbortSignal)=>request<OperatorDecisionResult>('operator/trial-requests/'+encodeURIComponent(id)+'/decision','POST',input,signal,true,key);
export const reconsiderOperatorTrial=(id:string,reason:string,key:string,signal?:AbortSignal)=>request<TrialRequest>('operator/trial-requests/'+encodeURIComponent(id)+'/reconsider','POST',{reason},signal,true,key);
export const reconcileOperatorTrial=(id:string,reason:string,key:string,signal?:AbortSignal)=>request<components['schemas']['ReconcileResult']>('operator/trial-operations/'+encodeURIComponent(id)+'/reconcile','POST',{reason},signal,true,key);
export const createOperatorTelegramTrial=(input:components['schemas']['OperatorTelegramTrialInput'],key:string,signal?:AbortSignal)=>request<OperatorTelegramTrialResult>('operator/clients/trial','POST',input,signal,true,key);
export const getOperatorSupport=(id:string,signal?:AbortSignal)=>request<SupportResult>(operatorClientPath(id)+'/support','GET',undefined,signal);
export const getOperatorSupportHistory=(id:string,before_sequence:number,signal?:AbortSignal)=>request<SupportResult>(operatorClientPath(id)+'/support/history','POST',{before_sequence},signal,true);
export function createOperatorSupportMessage(id:string,text:string,file:File|undefined,key:string,signal?:AbortSignal){if(!file)return request<SupportMessage>(operatorClientPath(id)+'/support/messages','POST',{text},signal,true,key);const body=new FormData();body.append('text',text);body.append('file',file);return request<SupportMessage>(operatorClientPath(id)+'/support/messages','POST',body,signal,true,key);}
export const acknowledgeOperatorSupport=(id:string,sequence:number,signal?:AbortSignal)=>request<void>(operatorClientPath(id)+'/support/read','POST',{sequence},signal,true);
export const setOperatorSupportState=(id:string,status:'open'|'closed',signal?:AbortSignal)=>request<void>(operatorClientPath(id)+'/support/state','POST',{status},signal,true);
export const setOperatorSupportBan=(id:string,banned:boolean,reason:string,signal?:AbortSignal)=>request<void>(operatorClientPath(id)+'/support/ban','POST',{banned,reason},signal,true);
