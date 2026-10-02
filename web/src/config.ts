export type PublicConfig={termsVersion:string;privacyVersion:string;termsURL:string;privacyURL:string;supportURL:string};
const env=import.meta.env;
export const config:PublicConfig={termsVersion:env.VITE_TERMS_VERSION??'',privacyVersion:env.VITE_PRIVACY_VERSION??'',termsURL:env.VITE_TERMS_URL??'',privacyURL:env.VITE_PRIVACY_URL??'',supportURL:env.VITE_SUPPORT_URL??''};
export function validateConfig(c:PublicConfig){
 if(!c.termsVersion||!c.privacyVersion)throw new Error('Missing policy versions');
 for(const value of [c.termsURL,c.privacyURL,c.supportURL]){const u=new URL(value);if(u.username||u.password||u.protocol!=='https:'&&!(value===c.supportURL&&u.protocol==='mailto:')||['t.me','telegram.me'].includes(u.hostname))throw new Error('Invalid public policy or support URL');}
}
