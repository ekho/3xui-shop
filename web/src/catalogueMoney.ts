export type Currency='RUB'|'USD'|'XTR';
const maxMinor=9223372036854775807n;

export function majorToMinor(value:string,currency:Currency){
 const normalized=value.trim().replace(',','.');
 const match=/^(0|[1-9][0-9]*)(?:\.([0-9]{1,2}))?$/.exec(normalized);
 if(!match||(currency==='XTR'&&match[2]))throw new Error('invalid_price');
 const amount=currency==='XTR'?BigInt(match[1]):BigInt(match[1])*100n+BigInt((match[2]??'').padEnd(2,'0'));
 if(amount>maxMinor)throw new Error('invalid_price');
 return amount.toString();
}

export function minorToMajor(value:string,currency:Currency){
 const amount=BigInt(value);
 if(currency==='XTR')return amount.toString();
 return `${amount/100n}.${String(amount%100n).padStart(2,'0')}`;
}

export function displayPrice(value:string,currency:Currency,lang:'ru'|'en'){
 const amount=BigInt(value),scale=currency==='XTR'?1n:100n;
 const whole=new Intl.NumberFormat(lang).format(amount/scale);
 return `${whole}${currency==='XTR'?'':(lang==='ru'?',':'.')+String(amount%scale).padStart(2,'0')} ${currency}`;
}
