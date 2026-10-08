import type {components} from './schema.gen';
import {isMiniApp,endMiniSession} from '../telegramSDK';
export type RegisterInput=components["schemas"]["RegisterInput"];
export type RegistrationAccepted=components["schemas"]["RegistrationAccepted"];
export type VerifyInput=components["schemas"]["VerifyInput"];
export type VerifyResult=components["schemas"]["VerifyResult"];
export type ResendInput=components["schemas"]["ResendInput"];
export type ResendAccepted=components["schemas"]["ResendAccepted"];
export type LoginInput=components["schemas"]["LoginInput"];
export type LoginResult=components["schemas"]["LoginResult"];
export type AccountResult=components["schemas"]["AccountResult"]|components["schemas"]["MiniAppAccountResult"];
export type TrialRequestInput=components["schemas"]["TrialRequestInput"];
export type TrialRequest=components["schemas"]["TrialRequest"];
export type CurrentTrialRequest=components["schemas"]["CurrentTrialRequest"];
export type Subscription=components["schemas"]["Subscription"];
export type SubscriptionKey=components["schemas"]["SubscriptionKey"];

export class ApiError extends Error {
 constructor(public status:number,public code:string,public request_id:string,public retryAfter=0){super(code);}
}
let csrf:string|undefined,miniToken:string|undefined;let miniEpoch=0;
async function request<T>(path:string,method='GET',body?:unknown,signal?:AbortSignal,sessionWrite=false,key?:string,blob=false):Promise<T>{
 const mini=isMiniApp(),epoch=miniEpoch;
 const form=body instanceof FormData;const headers:Record<string,string>={};if(body!==undefined&&!form)headers['Content-Type']='application/json';if(sessionWrite&&csrf)headers['X-CSRF-Token']=csrf;if(key)headers['Idempotency-Key']=key;if(mini&&miniToken)headers.Authorization='Bearer '+miniToken;
 let response:Response;
 try{response=await fetch('/api/v1/'+path,{method,body:body===undefined?undefined:form?body:JSON.stringify(body),headers,credentials:mini?'omit':'same-origin',cache:'no-store',signal:signal?AbortSignal.any([signal,AbortSignal.timeout(15000)]):AbortSignal.timeout(15000)});}catch(error){if(signal?.aborted)throw error;throw new ApiError(503,'SERVICE_UNAVAILABLE','');}
 signal?.throwIfAborted();if(mini&&epoch!==miniEpoch)throw new DOMException('Session ended','AbortError');
 if(!response.ok){if(response.status===401){csrf=undefined;if(mini&&miniToken){clearSession();endMiniSession();}}let failure:unknown;try{failure=await response.json();}catch{failure={};}const raw=failure as Partial<components['schemas']['APIError']>;const id=raw.error?.request_id??'';const delay=Number(response.headers.get('Retry-After'));throw new ApiError(response.status,raw.error?.code??'SERVICE_UNAVAILABLE',/^[0-9a-f-]{36}$/i.test(id)?id:'',Number.isFinite(delay)&&delay>0?Math.min(86400,Math.ceil(delay)):0);}
 if(response.status===204)return undefined as T;
 if(blob){const out=await response.blob();signal?.throwIfAborted();if(mini&&epoch!==miniEpoch)throw new DOMException('Session ended','AbortError');return out as T;}
 try{const out=await response.json() as T;signal?.throwIfAborted();if(mini&&epoch!==miniEpoch)throw new DOMException('Session ended','AbortError');return out;}catch{if(signal?.aborted)throw signal.reason;throw new ApiError(503,'SERVICE_UNAVAILABLE','');}
}
export const registerAccount=(input:RegisterInput)=>request<RegistrationAccepted>('auth/register','POST',input);
export const recordCampaignVisit=(code:string,signal?:AbortSignal)=>request<void>('campaign-visits','POST',{code},signal);
export const verifyEmail=(input:VerifyInput)=>request<VerifyResult>('auth/verify-email','POST',input);
export const resendVerification=(input:ResendInput)=>request<ResendAccepted>('auth/resend-verification','POST',input);
export async function loginAccount(input:LoginInput){const out=await request<LoginResult>('auth/login','POST',input);csrf=out.csrf_token;return out;}
export async function logoutAccount(){const mini=isMiniApp();try{await request<void>(mini?'telegram/mini-app/logout':'auth/logout','POST',undefined,undefined,true);}catch(reason){if(!mini&&reason instanceof ApiError&&reason.status===401){clearSession();return;}throw reason;}finally{if(mini){clearSession();endMiniSession('logout');}}if(!mini)clearSession();}
export async function getAccount(signal?:AbortSignal){const out=await request<AccountResult>(isMiniApp()?'telegram/mini-app/account':'me','GET',undefined,signal);csrf=out.csrf_token;return out;}
export async function loginMiniApp(input:components['schemas']['MiniAppSessionInput'],signal:AbortSignal){
 const out=await request<components['schemas']['MiniAppSessionResult']>('telegram/mini-app/session','POST',input,signal);
 signal.throwIfAborted();if(!/^mini_[A-Za-z0-9_-]{43}$/.test(out.session_token))throw new ApiError(503,'SERVICE_UNAVAILABLE','');
 miniToken=out.session_token;csrf=out.csrf_token;return out;
}
export const createTrialRequest=(input:TrialRequestInput,key:string,signal?:AbortSignal)=>request<TrialRequest>('trial-requests','POST',input,signal,true,key);
export const activateTelegramTrial=(key:string,signal?:AbortSignal)=>request<TrialRequest>('trials/activate','POST',{},signal,true,key);
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
export const clearSession=()=>{csrf=undefined;miniToken=undefined;miniEpoch++;};
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

export type IdentityContext=components['schemas']['IdentityContext'];
export type IdentityEmailPending=components['schemas']['IdentityEmailPending'];
export type TelegramLinkChallenge=components['schemas']['TelegramLinkChallenge'];
export const getIdentity=(signal?:AbortSignal)=>request<IdentityContext>('me/identity','GET',undefined,signal);
export const requestInitialEmail=(input:components['schemas']['InitialEmailInput'],signal:AbortSignal)=>request<RegistrationAccepted>('telegram/initial-email','POST',input,signal,true);
export const completeInitialEmail=(input:components['schemas']['InitialEmailCompleteInput'],signal:AbortSignal)=>request<VerifyResult>('telegram/initial-email/confirm','POST',input,signal,true);
export const startTelegramLink=(input:CurrentPasswordInput,signal:AbortSignal)=>request<TelegramLinkChallenge>('me/telegram/link','POST',input,signal,true);
export const confirmTelegramLink=(input:components['schemas']['MiniAppLinkInput'],signal:AbortSignal)=>request<components['schemas']['TelegramLinkResult']>('telegram/link','POST',input,signal);
export const unlinkTelegram=(input:CurrentPasswordInput,signal:AbortSignal)=>request<components['schemas']['TelegramUnlinkResult']>('me/telegram/unlink','POST',input,signal,true);

export type SupportAttachment=components['schemas']['SupportAttachment'];
export type SupportMessage=components['schemas']['SupportMessage'];
export type SupportConversation=components['schemas']['SupportConversation'];
export type SupportResult=components['schemas']['SupportResult'];
export const getSupport=(signal?:AbortSignal)=>request<SupportResult>('support','GET',undefined,signal);
export const getSupportHistory=(before_sequence:number,signal?:AbortSignal)=>request<SupportResult>('support/history','POST',{before_sequence},signal,true);
export function createSupportMessage(text:string,file:File|undefined,key:string,signal?:AbortSignal){if(!file)return request<SupportMessage>('support/messages','POST',{text},signal,true,key);const body=new FormData();body.append('text',text);body.append('file',file);return request<SupportMessage>('support/messages','POST',body,signal,true,key);}
export const acknowledgeSupport=(sequence:number,signal?:AbortSignal)=>request<void>('support/read','POST',{sequence},signal,true);
export const setSupportState=(status:'open'|'closed',signal?:AbortSignal)=>request<void>('support/state','POST',{status},signal,true);
export const downloadSupportAttachment=(id:string,signal?:AbortSignal)=>request<Blob>('support/messages/'+encodeURIComponent(id)+'/attachment','GET',undefined,signal,false,undefined,true);
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

export type CataloguePrice=components['schemas']['CataloguePrice'];
export type CatalogueTerms=components['schemas']['CatalogueTerms'];
export type CataloguePlanSnapshot=components['schemas']['CataloguePlanSnapshot'];
export type OperatorCataloguePlan=components['schemas']['OperatorCataloguePlan'];
export type CatalogueResult=components['schemas']['CatalogueResult'];
export type OperatorCatalogueResult=components['schemas']['OperatorCatalogueResult'];
export const getCatalogue=(signal?:AbortSignal)=>request<CatalogueResult>('catalogue','GET',undefined,signal);
export const getRenewalOffer=(signal?:AbortSignal)=>request<CataloguePlanSnapshot>('subscription/renewal','GET',undefined,signal);
export type PlanChangeContext=components['schemas']['PlanChangeContext'];
export const getPlanChangeContext=(signal?:AbortSignal)=>request<PlanChangeContext>('subscription/plan-change','GET',undefined,signal);
export type PaymentMethods=components['schemas']['PaymentMethods'];
export type PurchaseOrderInput=components['schemas']['PurchaseOrderInput'];
export type PurchaseOrder=components['schemas']['PurchaseOrder'];
export type CurrentPurchaseOrder=components['schemas']['CurrentPurchaseOrder'];
export type PurchaseReconcileInput=components['schemas']['PurchaseReconcileInput'];
export const getPaymentMethods=(signal?:AbortSignal)=>request<PaymentMethods>('payment-methods','GET',undefined,signal);
export const createPurchaseOrder=(input:PurchaseOrderInput,key:string,signal?:AbortSignal)=>request<PurchaseOrder>('orders','POST',input,signal,true,key);
export const getCurrentPurchaseOrder=(signal?:AbortSignal)=>request<CurrentPurchaseOrder>('orders/current','GET',undefined,signal);
export const getPurchaseOrder=(id:string,signal?:AbortSignal)=>request<PurchaseOrder>('orders/'+encodeURIComponent(id),'GET',undefined,signal);
export const cancelPurchaseOrder=(id:string,key:string,signal?:AbortSignal)=>request<PurchaseOrder>('orders/'+encodeURIComponent(id)+'/cancel','POST',{},signal,true,key);
export const getOperatorPurchaseOrder=(clientId:string,signal?:AbortSignal)=>request<CurrentPurchaseOrder>(operatorClientPath(clientId)+'/orders/current','GET',undefined,signal);
export const reconcilePurchaseOrder=(clientId:string,id:string,input:PurchaseReconcileInput,key:string,signal?:AbortSignal)=>request<PurchaseOrder>(operatorClientPath(clientId)+'/orders/'+encodeURIComponent(id)+'/reconcile','POST',input,signal,true,key);
export type ManualPaymentDecisionInput=components['schemas']['ManualPaymentDecisionInput'];
export type ManualPaymentPage=components['schemas']['ManualPaymentPage'];
export const reportManualPayment=(id:string,key:string,signal?:AbortSignal)=>request<PurchaseOrder>('orders/'+encodeURIComponent(id)+'/manual-report','POST',{},signal,true,key);
export const decideManualPayment=(clientId:string,id:string,input:ManualPaymentDecisionInput,key:string,signal?:AbortSignal)=>request<PurchaseOrder>(operatorClientPath(clientId)+'/orders/'+encodeURIComponent(id)+'/manual-decision','POST',input,signal,true,key);
export const getManualPaymentRequests=(after:string|null,signal?:AbortSignal)=>request<ManualPaymentPage>('operator/manual-payments'+(after?'?after='+encodeURIComponent(after):''),'GET',undefined,signal);
export type PaymentHistoryInput=components['schemas']['PaymentHistoryInput'];
export type PaymentHistoryPage=components['schemas']['PaymentHistoryPage'];
export const getPaymentHistory=(input:PaymentHistoryInput,signal?:AbortSignal)=>request<PaymentHistoryPage>('payment-history','POST',input,signal,true);
export const getOperatorPaymentHistory=(clientId:string,input:PaymentHistoryInput,signal?:AbortSignal)=>request<PaymentHistoryPage>(operatorClientPath(clientId)+'/payment-history','POST',input,signal,true);
export type PaymentCase=components['schemas']['PaymentCase'];
export type PaymentRefund=components['schemas']['PaymentRefund'];
export type PurchaseRefundInput=components['schemas']['PurchaseRefundInput'];
export const getOperatorPaymentCase=(clientId:string,orderId:string,receiptId?:string,signal?:AbortSignal)=>request<PaymentCase>(operatorClientPath(clientId)+'/orders/'+encodeURIComponent(orderId)+'/payment-case','POST',receiptId?{receipt_operation_id:receiptId}:{},signal,true);
export const confirmPurchaseRefund=(clientId:string,orderId:string,input:PurchaseRefundInput,key:string,signal?:AbortSignal)=>request<PaymentRefund>(operatorClientPath(clientId)+'/orders/'+encodeURIComponent(orderId)+'/refunds','POST',input,signal,true,key);
export const getOperatorCatalogue=(page:number,signal?:AbortSignal)=>request<OperatorCatalogueResult>('operator/catalogue?page='+page+'&per_page=50','GET',undefined,signal);
export const createCataloguePlan=(input:components['schemas']['CataloguePlanCreateInput'],key:string,signal?:AbortSignal)=>request<OperatorCataloguePlan>('operator/catalogue/plans','POST',input,signal,true,key);
export const reviseCataloguePlan=(id:string,input:components['schemas']['CataloguePlanRevisionInput'],key:string,signal?:AbortSignal)=>request<OperatorCataloguePlan>('operator/catalogue/plans/'+encodeURIComponent(id)+'/revision','POST',input,signal,true,key);
export const archiveCataloguePlan=(id:string,input:components['schemas']['CataloguePlanArchiveInput'],key:string,signal?:AbortSignal)=>request<OperatorCataloguePlan>('operator/catalogue/plans/'+encodeURIComponent(id)+'/archive','POST',input,signal,true,key);

export type Campaign=components['schemas']['Campaign'];
export type CampaignListResult=components['schemas']['CampaignListResult'];
export type CampaignDetail=components['schemas']['CampaignDetail'];
export type CampaignCreateInput=components['schemas']['CampaignCreateInput'];
export type CampaignStateInput=components['schemas']['CampaignStateInput'];
export const getOperatorCampaigns=(page:number,signal?:AbortSignal)=>request<CampaignListResult>('operator/campaigns/search','POST',{page,per_page:50},signal,true);
export const getOperatorCampaign=(id:string,signal?:AbortSignal)=>request<CampaignDetail>('operator/campaigns/'+encodeURIComponent(id),'GET',undefined,signal);
export const createCampaign=(input:CampaignCreateInput,key:string,signal?:AbortSignal)=>request<Campaign>('operator/campaigns','POST',input,signal,true,key);
export const setCampaignState=(id:string,input:CampaignStateInput,key:string,signal?:AbortSignal)=>request<Campaign>('operator/campaigns/'+encodeURIComponent(id)+'/state','POST',input,signal,true,key);

export type AccessOperationInput=components['schemas']['AccessOperationInput'];
export type AccessOperation=components['schemas']['AccessOperation'];
export type AccessReconcileInput=components['schemas']['AccessReconcileInput'];
const accessPath=(clientId:string)=>operatorClientPath(clientId)+'/access-operations';
export const createAccessOperation=(clientId:string,input:AccessOperationInput,key:string,signal?:AbortSignal)=>request<AccessOperation>(accessPath(clientId),'POST',input,signal,true,key);
export const getAccessOperation=(clientId:string,operationId:string,signal?:AbortSignal)=>request<AccessOperation>(accessPath(clientId)+'/'+encodeURIComponent(operationId),'GET',undefined,signal);
export const reconcileAccessOperation=(clientId:string,operationId:string,input:AccessReconcileInput,key:string,signal?:AbortSignal)=>request<AccessOperation>(accessPath(clientId)+'/'+encodeURIComponent(operationId)+'/reconcile','POST',input,signal,true,key);

export type OperatorRecoveryInput=components['schemas']['OperatorRecoveryInput'];
export type IdentityRecoveryAccepted=components['schemas']['IdentityRecoveryAccepted'];
export async function requestOperatorRecovery(clientId:string,input:OperatorRecoveryInput,key:string,signal?:AbortSignal){return request<IdentityRecoveryAccepted>('operator/clients/'+clientId+'/identity-recovery','POST',input,signal,true,key);}
export async function completeIdentityRecovery(input:components['schemas']['IdentityRecoveryCompleteInput'],signal?:AbortSignal){return request<VerifyResult>('auth/identity-recovery','POST',input,signal);}

export type StarsRefundInput=components['schemas']['StarsRefundInput'];
export type StarsSubscription=components['schemas']['StarsSubscription'];
export const starsSubscription=(signal?:AbortSignal)=>request<StarsSubscription>('stars-subscription','GET',undefined,signal);
export const controlStarsSubscription=(action:'cancel'|'resume',key:string,signal?:AbortSignal)=>request<StarsSubscription>('stars-subscription/control','POST',{action,confirmed:true},signal,true,key);
export const createStarsInvoice=(id:string,signal?:AbortSignal)=>request<PurchaseOrder>('orders/'+encodeURIComponent(id)+'/stars-invoice','POST',{},signal,true);
export const refundStarsPurchase=(clientId:string,orderId:string,input:StarsRefundInput,key:string,signal?:AbortSignal)=>request<components['schemas']['StarsRefund']>(operatorClientPath(clientId)+'/orders/'+encodeURIComponent(orderId)+'/stars-refund','POST',input,signal,true,key);
