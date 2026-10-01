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
 const headers:Record<string,string>={};if(body!==undefined)headers['Content-Type']='application/json';if(sessionWrite&&csrf)headers['X-CSRF-Token']=csrf;if(key)headers['Idempotency-Key']=key;
 let response:Response;
 try{response=await fetch('/api/v1/'+path,{method,body:body===undefined?undefined:JSON.stringify(body),headers,credentials:'same-origin',cache:'no-store',signal:signal?AbortSignal.any([signal,AbortSignal.timeout(15000)]):AbortSignal.timeout(15000)});}catch(error){if(signal?.aborted)throw error;throw new ApiError(503,'SERVICE_UNAVAILABLE','');}
 if(!response.ok){let failure:unknown;try{failure=await response.json();}catch{failure={};}const raw=failure as Partial<components['schemas']['APIError']>;const id=raw.error?.request_id??'';const delay=Number(response.headers.get('Retry-After'));throw new ApiError(response.status,raw.error?.code??'SERVICE_UNAVAILABLE',/^[0-9a-f-]{36}$/i.test(id)?id:'',Number.isFinite(delay)&&delay>0?Math.min(86400,Math.ceil(delay)):0);}
 if(response.status===204)return undefined as T;
 try{return await response.json() as T;}catch{throw new ApiError(503,'SERVICE_UNAVAILABLE','');}
}
export const registerAccount=(input:RegisterInput)=>request<RegistrationAccepted>('auth/register','POST',input);
export const verifyEmail=(input:VerifyInput)=>request<VerifyResult>('auth/verify-email','POST',input);
export const resendVerification=(input:ResendInput)=>request<ResendAccepted>('auth/resend-verification','POST',input);
export async function loginAccount(input:LoginInput){const out=await request<LoginResult>('auth/login','POST',input);csrf=out.csrf_token;return out;}
export async function logoutAccount(){await request<void>('auth/logout','POST',undefined,undefined,true);csrf=undefined;}
export async function getAccount(signal?:AbortSignal){const out=await request<AccountResult>('me','GET',undefined,signal);csrf=out.csrf_token;return out;}
export const createTrialRequest=(input:TrialRequestInput,key:string,signal?:AbortSignal)=>request<TrialRequest>('trial-requests','POST',input,signal,true,key);
export const getCurrentTrialRequest=(signal?:AbortSignal)=>request<CurrentTrialRequest>('trial-requests/current','GET',undefined,signal);
export const getSubscription=(signal?:AbortSignal)=>request<Subscription>('subscription','GET',undefined,signal);
export const getSubscriptionKey=(signal?:AbortSignal)=>request<SubscriptionKey>('subscription/key','GET',undefined,signal);
