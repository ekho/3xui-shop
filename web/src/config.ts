export type PublicConfig={termsVersion:string;privacyVersion:string;termsURL:string;privacyURL:string;supportURL:string};
export let config:PublicConfig;
export function validateConfig(value:unknown):asserts value is PublicConfig{
 if(!value||typeof value!=='object')throw new Error('Invalid public config');
 const c=value as Record<string,unknown>;
 for(const key of ['termsVersion','privacyVersion'])if(typeof c[key]!=='string'||!(c[key] as string).trim()||(c[key] as string).length>128)throw new Error('Missing or invalid policy version');
 for(const key of ['termsURL','privacyURL','supportURL']){
  const value=c[key];if(typeof value!=='string'||value.length>2048)throw new Error('Invalid public URL');
  const u=new URL(value);if(u.username||u.password||u.protocol!=='https:'&&!(key==='supportURL'&&u.protocol==='mailto:')||['t.me','telegram.me'].includes(u.hostname))throw new Error('Invalid public policy or support URL');
 }
}
export async function loadConfig(){
 const response=await fetch('/config.json',{cache:'no-store'});
 if(!response.ok)throw new Error('Missing public config');
 const body=await response.text();if(body.length>8192)throw new Error('Oversized public config');
 const value:unknown=JSON.parse(body);validateConfig(value);config=value;
}
