import {lazy,Suspense,useEffect,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {BrowserRouter,Route,Routes} from 'react-router-dom';
import {Auth} from './Auth';
import {Security} from './Security';
import {Cabinet} from './Cabinet';
import {Catalogue} from './Catalogue';
import {Support} from './Support';
import {config,loadConfig} from './config';
import {text,link,type Lang} from './i18n';
import './style.css';
const OperatorAdmin=lazy(()=>import('./Admin'));
const path=location.pathname;
if(path==='/admin')history.replaceState(null,'','/admin/clients'+location.search);
if(!['/verify-email','/reset-password','/confirm-email-change'].includes(path))delete window.__emailToken;
else window.addEventListener('hashchange',()=>location.reload());
function App(){
 const[lang,setLang]=useState<Lang>(new URLSearchParams(location.search).get('lang')==='en'?'en':'ru');const t=text(lang);const admin=path.startsWith('/admin');
 useEffect(()=>{document.documentElement.lang=lang;document.title=admin?t.operatorCabinet:path==='/catalogue'?t.plans:path==='/cabinet/support'?t.supportMessages:t.cabinet;},[admin,lang,t]);
 const regular=path==='/catalogue'?<Catalogue lang={lang}/>:path==='/cabinet/support'?<Support lang={lang}/>:path==='/cabinet/security'?<Security lang={lang}/>:path==='/cabinet'||path==='/'?<Cabinet lang={lang}/>:<Auth mode={path==='/register'?'register':path==='/verify-email'?'verify-email':path==='/forgot-password'?'forgot-password':path==='/reset-password'?'reset-password':path==='/confirm-email-change'?'confirm-email-change':'login'} lang={lang}/>;
 return <><header><a href={link(admin?'/admin':'/cabinet',lang)} className="brand">{admin?t.operatorCabinet:t.cabinet}</a><div aria-label="Language"><button type="button" aria-pressed={lang==='ru'} onClick={()=>setLang('ru')}>RU</button><button type="button" aria-pressed={lang==='en'} onClick={()=>setLang('en')}>EN</button></div></header>{admin?<div className="admin-root"><Routes><Route path="/admin/*" element={<Suspense fallback={<p role="status">{t.loading}</p>}><OperatorAdmin lang={lang}/></Suspense>}/></Routes></div>:<main>{regular}</main>}<footer><a href={config.termsURL}>{t.termsLink}</a><a href={config.privacyURL}>{t.privacyLink}</a></footer></>;
}
const root=createRoot(document.getElementById('root')!);
root.render(<main><p role="status">Загрузка… / Loading…</p></main>);
void loadConfig().then(()=>root.render(<BrowserRouter><App/></BrowserRouter>)).catch(()=>root.render(<main><p role="alert">Сервис не настроен. / Service is not configured.</p></main>));
