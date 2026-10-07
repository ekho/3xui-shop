import {useEffect,useRef,useState} from 'react';
import {useLocation} from 'react-router-dom';
import QRCode from 'qrcode';
import {text,otherImportText,type Lang} from './i18n';

type Platform='ios'|'android'|'macos'|'windows'|'other';
const downloads:Record<Exclude<Platform,'ios'|'other'>,string>={
 android:'https://play.google.com/store/apps/details?id=com.happproxy',
 macos:'https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.macOS.universal.dmg',
 windows:'https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.x64.exe'
};
const ios={ru:'https://apps.apple.com/ru/app/happ-lite/id6799917773',global:'https://apps.apple.com/us/app/happ-proxy-utility/id6504287215'};
const catalog='https://www.happ.su/main';
const external={target:'_blank',rel:'noreferrer noopener'} as const;

export function Connection({lang,subscriptionURL,busy,onReveal,onHide}:{lang:Lang;subscriptionURL:string;busy:boolean;onReveal:()=>void;onHide:()=>void}){
 const key=subscriptionURL;
 const requested=new URLSearchParams(useLocation().search).get('platform');const selected=requested&&['ios','android','macos','windows','other'].includes(requested)?requested as Platform:undefined;
 const t=text(lang);const[platform,setPlatform]=useState<Platform>(selected??'ios');const[region,setRegion]=useState<keyof typeof ios>('global');const[copyStatus,setCopyStatus]=useState('');const[showQR,setShowQR]=useState(false);const[qrReady,setQrReady]=useState(false);const[qrError,setQrError]=useState('');const input=useRef<HTMLInputElement>(null);const canvas=useRef<HTMLCanvasElement>(null);
 useEffect(()=>{if(selected)setPlatform(selected);},[selected]);
 useEffect(()=>{setCopyStatus('');setShowQR(false);setQrReady(false);setQrError('');if(canvas.current){canvas.current.width=0;canvas.current.height=0;}},[key]);
 useEffect(()=>{if(!showQR||!key)return;let current=true;const scratch=document.createElement('canvas');setQrReady(false);setQrError('');
  void QRCode.toCanvas(scratch,key,{errorCorrectionLevel:'M',margin:2,width:220}).then(()=>{const target=canvas.current;if(!current||!target)return;target.width=scratch.width;target.height=scratch.height;const context=target.getContext('2d');if(!context)throw new Error('canvas');context.drawImage(scratch,0,0);setQrReady(true);}).catch(()=>{if(current)setQrError(t.qrUnavailable);});
  return()=>{current=false;if(canvas.current){canvas.current.width=0;canvas.current.height=0;}};
 },[key,showQR,t.qrUnavailable]);
 async function copy(){try{await navigator.clipboard.writeText(key);setCopyStatus(t.copied);}catch{setCopyStatus(t.manualCopy);input.current?.focus();input.current?.select();}}
 const install=platform==='ios'?ios[region]:platform==='other'?catalog:downloads[platform];
 return <section className="connection" aria-labelledby="connection-title"><h2 id="connection-title">{t.connectDevice}</h2>
  <label htmlFor="platform">{t.platform}<select id="platform" value={platform} onChange={event=>setPlatform(event.target.value as Platform)}><option value="ios">iOS</option><option value="android">Android</option><option value="macos">macOS</option><option value="windows">Windows</option><option value="other">{t.other}</option></select></label>
  {platform==='ios'?<><label htmlFor="store-region">{t.storeRegion}<select id="store-region" value={region} onChange={event=>setRegion(event.target.value as keyof typeof ios)}><option value="global">{t.globalStore}</option><option value="ru">{t.russiaStore}</option></select></label>{region==='ru'?<p className="help">{t.ruStoreWarning}</p>:null}</>:null}
  <p><a className="button-link" href={install} {...external}>{platform==='other'?t.openCatalog:t.installHapp}</a></p>
  {platform==='ios'?<p className="secondary-links"><a href={ios.global} {...external}>{t.globalAppStore}</a><a href={catalog} {...external}>{t.developerCatalog}</a></p>:null}
  <ol aria-label={t.connectionSteps}><li>{t.installStep}</li><li>{platform==='other'?otherImportText(lang):t.importStep}</li><li>{t.connectStep}</li></ol>
  {!key?<div className="connection-actions" aria-busy={busy}>{busy?<p role="status">{t.loading}</p>:<button className="primary" onClick={onReveal}>{t.showKey}</button>}{busy?<button onClick={onHide}>{t.hideConnection}</button>:null}</div>:<div className="connection-secret"><label htmlFor="subscription-key">{t.key}<input ref={input} id="subscription-key" value={key} readOnly/></label><div className="connection-actions"><button onClick={copy}>{t.copy}</button><button onClick={()=>setShowQR(true)}>{t.showQR}</button>{platform!=='other'?<a className="button-link" href={'happ://add/'+key}>{t.openHapp}</a>:null}<button onClick={onHide}>{t.hideConnection}</button></div><p role="status">{copyStatus}</p>{showQR?<div className="qr"><canvas ref={canvas} role="img" aria-label={t.qrLabel} hidden={!qrReady}/>{!qrReady&&!qrError?<p role="status">{t.loading}</p>:null}{qrError?<p role="alert">{qrError}</p>:null}</div>:null}<p>{t.importHelp}</p></div>}
 </section>;
}
