import {useEffect,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {Auth} from './Auth';
import {Security} from './Security';
import {Cabinet} from './Cabinet';
import {Support} from './Support';
import {config,validateConfig} from './config';
import {text,link,type Lang} from './i18n';
import './style.css';
const path=location.pathname;
if(!['/verify-email','/reset-password','/confirm-email-change'].includes(path))delete window.__emailToken;
else window.addEventListener('hashchange',()=>location.reload());
function App(){const[lang,setLang]=useState<Lang>(new URLSearchParams(location.search).get('lang')==='en'?'en':'ru');const t=text(lang);useEffect(()=>{document.documentElement.lang=lang;document.title=path==='/cabinet/support'?t.supportMessages:t.cabinet;},[lang,t]);return <><header><a href={link('/cabinet',lang)} className="brand">{t.cabinet}</a><div aria-label="Language"><button type="button" aria-pressed={lang==='ru'} onClick={()=>setLang('ru')}>RU</button><button type="button" aria-pressed={lang==='en'} onClick={()=>setLang('en')}>EN</button></div></header><main>{path==='/cabinet/support'?<Support lang={lang}/>:path==='/cabinet/security'?<Security lang={lang}/>:path==='/cabinet'||path==='/'?<Cabinet lang={lang}/>:<Auth mode={path==='/register'?'register':path==='/verify-email'?'verify-email':path==='/forgot-password'?'forgot-password':path==='/reset-password'?'reset-password':path==='/confirm-email-change'?'confirm-email-change':'login'} lang={lang}/>}</main><footer><a href={config.termsURL}>{t.termsLink}</a><a href={config.privacyURL}>{t.privacyLink}</a></footer></>}
const root=createRoot(document.getElementById('root')!);
try{validateConfig(config);root.render(<App/>);}catch{root.render(<main><p role="alert">Сервис не настроен. / Service is not configured.</p></main>);}
