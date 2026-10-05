"""Own Docker acceptance stack. Secrets stay in private files; Telegram is opt-in."""
import base64
import argparse
import http.cookiejar
from ipaddress import IPv4Address
import json
import os
from pathlib import Path
import re
import secrets
import ssl
import subprocess
import sys
import time
import urllib.request
from urllib.error import HTTPError
from urllib.parse import urlsplit, parse_qs, urlencode
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
PROFILE = os.environ.get('LOCAL_PROFILE', 'native')
assert PROFILE in ('native', 'legacy'), 'invalid local profile'
STATE = Path(os.environ.get('LOCAL_STATE_DIR', ROOT / '.superpowers/acceptance' / ('native-docker' if PROFILE == 'native' else 'local-docker')))
ENV = STATE / 'public.env'
METADATA = json.loads((STATE / 'runtime.json').read_text()) if (STATE / 'runtime.json').exists() else {}
PROJECT = METADATA.get('project', 'cabinet-native' if PROFILE == 'native' else 'cabinet-local')
PG_USER = METADATA.get('postgres_user', 'cabinet')
BASE_DATABASE = METADATA.get('base_database', 'cabinet')
VPN_ORIGIN_MARKER = b'cabinet-local-vpn-ok'
assert re.fullmatch(r'[a-z0-9][a-z0-9-]*', PROJECT), 'invalid owned project'
assert all(re.fullmatch(r'[a-z][a-z0-9_]*', n) for n in (PG_USER, BASE_DATABASE)), 'invalid owned database identity'

def own_database_name(name):
    return name == BASE_DATABASE or re.fullmatch(re.escape(BASE_DATABASE) + r'_restore_(?:[a-z][a-z0-9_]*_)?[0-9a-f]{32}', name) is not None

def restore_name(purpose=''):
    assert not purpose or re.fullmatch(r'[a-z]+(?:-[a-z]+)*', purpose), 'invalid restore purpose'
    return BASE_DATABASE + '_restore_' + (purpose.replace('-', '_') + '_' if purpose else '') + uuid4().hex

def acceptance_state(purpose):
    assert re.fullmatch(r'[a-z]+(?:-[a-z]+)*', purpose), 'invalid acceptance purpose'
    return ROOT / '.superpowers/acceptance' / purpose

def fixture_prefix(purpose):
    acceptance_state(purpose)
    prefix = METADATA.get('fixture_prefixes', {}).get(purpose, purpose + '-')
    assert re.fullmatch(r'[a-z][a-z0-9-]*-', prefix), 'invalid owned fixture prefix'
    return prefix

def fixture_pattern(purpose):
    return fixture_prefix(purpose) + '%@example.test'
ORIGIN = 'https://localhost:58443'

def write(name, value):
    path = STATE / name
    path.write_text(value)
    path.chmod(0o600)
    return str(path)

def command(args, *, stdin=None):
    result = subprocess.run(args, cwd=ROOT, input=stdin, capture_output=True, timeout=180)
    if result.returncode:
        # Provider output may contain credentials, subscription links or test identity.
        raise RuntimeError('local command failed: ' + args[0])
    return result.stdout

def compose(*args, stdin=None):
    files=['-f','deploy/acceptance/compose.acceptance.yml','-f','deploy/acceptance/compose.local.yml']
    if PROFILE == 'native':files+=['-f','deploy/acceptance/compose.native.yml']
    return command(['docker','compose','--project-name',PROJECT,
                    *(['--profile','restore'] if PROFILE == 'legacy' else []),
                    '--env-file',str(ENV),*files,*args], stdin=stdin)

def prepare():
    STATE.mkdir(parents=True, exist_ok=True)
    STATE.chmod(0o700)
    if not (STATE / 'runtime.json').exists():
        write('runtime.json', json.dumps({'project': PROJECT, 'postgres_user': PG_USER,
              'base_database': BASE_DATABASE, 'fixture_prefixes': {}}))
    (STATE / 'origin').mkdir(exist_ok=True)
    (STATE / 'origin/index.html').write_bytes(VPN_ORIGIN_MARKER)
    if not (STATE/'native-operator-account').exists():write('native-operator-account','')
    if ENV.exists():
        return
    (STATE / 'panel-db').mkdir(mode=0o700)
    values = {name:secrets.token_urlsafe(32) for name in ('pg-password','adapter-token','smtp-password','panel-password','unused-panel-token')}
    values.update({'mail-key':base64.b64encode(secrets.token_bytes(32)).decode(),
                   'code-key':base64.b64encode(secrets.token_bytes(32)).decode(),
                   'redis-url':'redis://redis:6379/0',
                   'database-url':'postgres://'+PG_USER+':'+values['pg-password']+'@postgres:5432/'+BASE_DATABASE+'?sslmode=disable',
                   'unused-bot-token':'1:'+secrets.token_urlsafe(32),
                   'smtp-auth':'local-service:'+values['smtp-password']})
    for name, value in values.items():
        write(name, value)
    cert, key = STATE/'cert.pem', STATE/'key.pem'
    command(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','30','-subj','/CN=localhost',
             '-addext','subjectAltName=DNS:localhost,DNS:gateway,DNS:mailpit,DNS:panel,IP:127.0.0.1',
             '-keyout',str(key),'-out',str(cert)])
    key.chmod(0o600)
    write('terms','Local acceptance terms fixture. No real service or purchase.\n')
    write('privacy','Local acceptance privacy fixture. Use test data only.\n')
    write('vpn.json','{}')
    config = dict(line.split('=',1) for line in (ROOT/'deploy/acceptance/.env.example').read_text().splitlines() if line and not line.startswith('#'))
    config.update(PG_USER=PG_USER,BASE_DATABASE=BASE_DATABASE,CABINET_HOST='localhost',CABINET_ORIGIN='https://localhost:58443',
                  TERMS_URL='https://localhost:58443/terms',PRIVACY_URL='https://localhost:58443/privacy',
                  APP_RUNTIME_UID=str(os.getuid()),APP_RUNTIME_GID=str(os.getgid()),
                  APP_NETWORK_SUBNET='172.31.96.0/28' if PROFILE == 'native' else '172.31.99.0/28',
                  APP_GATEWAY_IP='172.31.96.14' if PROFILE == 'native' else '172.31.99.14',
                  SUBSCRIPTION_BASE_URL='https://localhost:59445/sub/',LOCAL_STATE_DIR=str(STATE),
                  PANEL_PASSWORD_FILE=str(STATE/'panel-password'),SMTP_AUTH_FILE=str(STATE/'smtp-auth'))
    mapping={'PG_PASSWORD_FILE':'pg-password','DATABASE_URL_FILE':'database-url','REDIS_URL_FILE':'redis-url',
             'MAIL_KEY_FILE':'mail-key','CODE_KEY_FILE':'code-key','BOT_ADAPTER_TOKEN_FILE':'adapter-token',
             'SMTP_PASSWORD_FILE':'smtp-password','PANEL_TOKEN_FILE':'unused-panel-token','BOT_TOKEN_FILE':'unused-bot-token'}
    for name, file in mapping.items():
        config[name]=str(STATE/file)
    for name in config:
        if name.endswith('_CA_FILE') or name in ('PUBLIC_CERT_FILE','ADAPTER_CERT_FILE'):
            config[name]=str(cert)
        elif name in ('PUBLIC_KEY_FILE','ADAPTER_KEY_FILE'):
            config[name]=str(key)
    write('public.env','\n'.join(name+'='+value for name,value in config.items())+'\n')

def session():
    context=ssl.create_default_context(cafile=str(STATE/'cert.pem'))
    return urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),urllib.request.HTTPSHandler(context=context))

def request(opener, url, body=None, headers=None):
    data=None if body is None else json.dumps(body).encode()
    req=urllib.request.Request(url,data=data,headers={'Content-Type':'application/json','Accept-Language':'en-US',**(headers or {})})
    with opener.open(req,timeout=15) as response:
        return response.status, response.headers, response.read()

def panel_call(opener, path, body=None, csrf=None):
    status, _, raw=request(opener,'https://localhost:59444/'+path,body,{'X-CSRF-Token':csrf} if csrf else {})
    result=json.loads(raw)
    if status!=200 or result.get('success') is not True:
        raise RuntimeError('panel rejected '+path.split('/')[0])
    return result.get('obj')

def login_panel():
    opener=session()
    csrf=panel_call(opener,'csrf-token')
    password=(STATE/'panel-password').read_text().strip()
    initialized=STATE/'panel-initialized'
    if not initialized.exists():
        # Only the newly created, isolated panel DB uses upstream's initial credentials.
        panel_call(opener,'login',{'username':'admin','password':'admin'},csrf)
        panel_call(opener,'panel/api/setting/updateUser',{'oldUsername':'admin','oldPassword':'admin','newUsername':'local-operator','newPassword':password},csrf)
        write('panel-initialized','yes')
    panel_call(opener,'login',{'username':'local-operator','password':password},csrf)
    return opener, csrf

def allow_test_origin(opener, csrf):
    cid=compose('ps','-q','origin').decode().strip()
    networks=json.loads(command(['docker','inspect',cid]))[0]['NetworkSettings']['Networks']
    address=networks[PROJECT+'_default']['IPAddress']
    rule=[{'action':'allow','network':'tcp','ip':[str(IPv4Address(address))+'/32'],'port':'8000'}]
    config=json.loads(panel_call(opener,'panel/api/xray/',{},csrf))['xraySetting']
    direct=next(o for o in config['outbounds'] if o.get('tag')=='direct' and o.get('protocol')=='freedom')
    settings=direct.setdefault('settings',{})
    if settings.get('finalRules')==rule:
        return
    # Only the owned test origin bypasses Xray's native private-IP fallback.
    settings['finalRules']=rule
    req=urllib.request.Request('https://localhost:59444/panel/api/xray/update',
        data=urlencode({'xraySetting':json.dumps(config),'outboundTestUrl':'http://origin:8000/'}).encode(),
        headers={'Content-Type':'application/x-www-form-urlencoded','X-CSRF-Token':csrf,'Accept-Language':'en-US'})
    with opener.open(req,timeout=15) as response:
        assert response.status==200 and json.loads(response.read()).get('success') is True, 'test origin rule rejected'

def up(reuse_images=False):
    prepare()
    write('public.env',ENV.read_text().replace('TRIAL_ENABLED=true','TRIAL_ENABLED=false'))
    compose('config','--quiet')
    # Public legal/support values are runtime configuration; secrets remain files.
    if not reuse_images:compose('build','backend','gateway',*(['bot'] if PROFILE == 'legacy' else []))
    compose('up','--pull','missing','--no-build','-d','backend','gateway','origin')
    deadline=time.monotonic()+30
    while True:
        try:
            panel_call(session(),'csrf-token')
            break
        except (OSError,ValueError):
            if time.monotonic()>deadline:
                raise RuntimeError('panel HTTPS not ready') from None
            time.sleep(.2)
    opener, csrf=login_panel()
    allow_test_origin(opener,csrf)
    inbounds=panel_call(opener,'panel/api/inbounds/list')
    if not any(row.get('tag')=='local-regular-vless' for row in inbounds):
        panel_call(opener,'panel/api/inbounds/add',{
            'enable':True,'remark':'Local acceptance','listen':'0.0.0.0','port':24443,'protocol':'vless','tag':'local-regular-vless',
            'settings':{'clients':[],'decryption':'none','fallbacks':[]},
            'streamSettings':{'network':'tcp','security':'tls','tlsSettings':{'serverName':'panel','certificates':[{'certificateFile':'/run/secrets/public_cert','keyFile':'/run/secrets/public_key'}]}},
            'sniffing':{'enabled':False},'allocate':{'strategy':'always','refresh':5,'concurrency':3}} ,csrf)
    # Preserve absence as a version-specific observation before enabling trial.
    _,_,raw=request(opener,'https://localhost:59444/panel/api/clients/get/acct_'+secrets.token_hex(16))
    absent=json.loads(raw)
    write('panel-absence.json',json.dumps(absent))
    assert absent.get('success') is False and absent.get('msg') == 'Obtain (record not found)' and absent.get('obj') is None, 'unsupported absence response'
    preflight()
    data=ENV.read_text().replace('TRIAL_ENABLED=false','TRIAL_ENABLED=true')
    write('public.env',data)
    compose('up','--no-build','-d','backend')
    wait_until(ready)
    print('PASS: local 3X-UI3.7.0, TLS Mailpit, cabinet HTTPS; '+PROFILE+' process profile',flush=True)

def api(opener, path, body=None, csrf=None, key=None):
    headers={'Origin':ORIGIN}
    if csrf: headers['X-CSRF-Token']=csrf
    if key: headers['Idempotency-Key']=key
    status, headers, raw=request(opener,ORIGIN+path,body,headers)
    return status, headers, json.loads(raw) if raw else None

def actor(path, body, key=None):
    assert PROFILE == 'legacy', 'Python adapter is restricted to legacy acceptance'
    # This uses the shipped Python adapter, but does not contact Telegram.
    script='''import asyncio,json,sys
from pathlib import Path
from app.bot.services.web_trial import WebTrialAdapter
async def main():
    data=json.load(sys.stdin)
    a=WebTrialAdapter('https://gateway:9443',Path('/run/secrets/adapter_token').read_text().strip(),{101},'/run/secrets/adapter_ca')
    try: print(json.dumps(await a._request(data['path'],data['body'],data['key'])))
    finally: await a.close()
asyncio.run(main())
'''
    return json.loads(compose('run','--rm','--no-deps','-T','--entrypoint','python','bot','-c',script,
                              stdin=json.dumps({'path':path,'body':body,'key':key}).encode()))

def wait_until(check, timeout=30):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        value=check()
        if value:return value
        time.sleep(.2)
    raise RuntimeError('local condition timed out')

def ready():
    try:return request(session(),ORIGIN+'/healthz')[0]==200
    except HTTPError as error:
        if error.code in (502,503):return False
        raise
    except OSError:return False

def maintenance_ready():
    try:
        request(session(),ORIGIN+'/healthz')
        raise AssertionError('cabinet ingress remains open')
    except HTTPError as error:
        assert error.code==503, 'maintenance must close cabinet ingress'
        return True
    except OSError:return False

def internal_ready():
    try:return json.loads(compose('exec','-T','gateway','wget','-qO-','http://backend:8080/healthz'))=={'ok':True}
    except (RuntimeError,ValueError):return False

def mail_token(email, purpose='/verify-email'):
    assert email.endswith('@example.test'), 'test recipient required'
    _,_,raw=request(session(),'https://localhost:59446/api/v1/messages')
    for row in json.loads(raw)['messages']:
        if any(recipient['Address']==email for recipient in row['To']):
            _,_,raw=request(session(),'https://localhost:59446/api/v1/message/'+row['ID'])
            text=json.loads(raw)['Text']
            if purpose in text:
                match=re.search(r'#token=([A-Za-z0-9_-]{43})',text)
                if match:return match[1]

def signup():
    wait_until(ready)
    opener=session()
    email='acceptance-'+uuid4().hex+'@example.test'
    status,_,_=api(opener,'/api/v1/auth/register',{'email':email,'locale':'en','accepted_terms_version':'1','accepted_privacy_version':'1'})
    assert status==202, 'registration failed'
    token=wait_until(lambda:mail_token(email))
    password='Local acceptance '+secrets.token_urlsafe(20)
    assert api(opener,'/api/v1/auth/verify-email',{'token':token,'new_password':password})[0]==200
    status,_,login=api(opener,'/api/v1/auth/login',{'email':email,'password':password})
    assert status==200
    status,_,trial=api(opener,'/api/v1/trial-requests',{'comment':'Isolated Docker acceptance'},login['csrf_token'],str(uuid4()))
    assert status==201
    return opener,trial,{'email':email,'password':password}

def approve(trial):
    return actor('/internal/v1/trial-requests/'+trial['request_id']+'/decision',
                 {'operator_tg_id':101,'decision':'approve','callback_query_id':'local-'+trial['request_id']})

def active(opener):
    _,_,value=api(opener,'/api/v1/subscription')
    if value['status']=='needs_review':raise RuntimeError('panel issuance needs review')
    return value if value['status']=='active' else None

def database():
    name=urlsplit((STATE/'database-url').read_text().strip()).path.lstrip('/')
    assert own_database_name(name), 'not an owned test database'
    return name

def sql(query, db=None, operation=None):
    args=['exec','-T','postgres','psql','-U',PG_USER,'-d',db or database(),'-qAt','-v','ON_ERROR_STOP=1']
    if operation:args+=['-v','op='+str(UUID(operation))]
    return compose(*args,stdin=query.encode()).decode().strip()

def snapshot(operation, db=None):
    return json.loads(sql("""SELECT json_build_object('target',o.target,'status',o.status,
      'grant',g.status,'grants',(SELECT count(*) FROM trial_grants WHERE operation_id=o.id),
      'job',(SELECT state FROM river_job WHERE kind='trial_provision' AND args->>'operation_id'=o.id::text ORDER BY id DESC LIMIT 1))
      FROM trial_operations o JOIN trial_grants g ON g.operation_id=o.id WHERE o.id=:'op';""",db,operation))

def panel_readback(target):
    panel,_=login_panel()
    obj=panel_call(panel,'panel/api/clients/get/'+target['panel_key'])
    c=obj['client']
    limit=target['device_count']+1 if target['device_count'] else 0
    assert c['uuid']==target['vpn_id'] and c['subId']==target['sub_id']
    assert c['expiryTime']==target['expiry_time_ms'] and c['limitIp']==limit and c['totalGB']==target['traffic_limit_bytes']
    assert sorted(obj['inboundIds'])==sorted(target['inbound_ids'])
    return {k:c[k] for k in ('id','uuid','subId','expiryTime','limitIp','totalGB','enable')}

def preflight():
    panel,csrf=login_panel()
    inbounds=panel_call(panel,'panel/api/inbounds/list')
    regular=next(row for row in inbounds if row['tag']=='local-regular-vless')
    probe=next((row for row in inbounds if row['tag']=='local-probe-vless'),None)
    if probe is None:
        body={k:regular[k] for k in ('enable','listen','protocol','settings','streamSettings','sniffing')}
        body.update(port=24444,tag='local-probe-vless',remark='Disposable compatibility probes')
        body['settings']={**body['settings'],'clients':[]}
        probe=panel_call(panel,'panel/api/inbounds/add',body,csrf)
    email='probe_'+uuid4().hex
    other='probe_'+uuid4().hex
    client={'email':email,'id':str(uuid4()),'subId':secrets.token_hex(8),'expiryTime':int(time.time()*1000)+3600000,'enable':True,'totalGB':1024**3,'limitIp':2,'flow':'xtls-rprx-vision'}
    try:
        panel_call(panel,'panel/api/clients/add',{'client':client,'inboundIds':[regular['id']]},csrf)
        before=panel_call(panel,'panel/api/clients/get/'+email)
        def rejected(candidate,ids):
            _,_,raw=request(panel,'https://localhost:59444/panel/api/clients/add',{'client':candidate,'inboundIds':ids},{'X-CSRF-Token':csrf})
            value=json.loads(raw)
            assert type(value.get('success')) is bool
            return not value['success']
        duplicate_key=rejected({**client,'id':str(uuid4())},[regular['id']])
        existing=panel_call(panel,'panel/api/clients/get/'+email)
        key_changed_identity=existing['client']['uuid']!=before['client']['uuid']
        client['id']=existing['client']['uuid']
        duplicate_uuid=rejected({**client,'email':other,'subId':secrets.token_hex(8)},[probe['id']])
        if not duplicate_uuid:panel_call(panel,'panel/api/clients/del/'+other,{},csrf)
        before=panel_call(panel,'panel/api/clients/get/'+email)
        panel_call(panel,'panel/api/clients/'+email+'/attach',{'inboundIds':[probe['id']]},csrf)
        after=panel_call(panel,'panel/api/clients/get/'+email)
        fields=('uuid','subId','expiryTime','limitIp','totalGB','enable','flow','password')
        assert all(before['client'].get(k)==after['client'].get(k) for k in fields) and sorted(after['inboundIds'])==sorted([regular['id'],probe['id']])
        result={'duplicate_key_rejected':duplicate_key,'duplicate_key_changes_identity':key_changed_identity,'duplicate_uuid_across_inbounds_rejected':duplicate_uuid,'attach_preserved':True}
        write('panel-preflight.json',json.dumps(result))
        print('PASS: native panel preflight',json.dumps(result),'; automatic uncertain-create retry remains disabled',flush=True)
    finally:
        panel_call(panel,'panel/api/clients/del/'+email,{},csrf)  # Only this generated disposable probe.

def vpn(opener):
    status,headers,key=api(opener,'/api/v1/subscription/key')
    assert status==200 and headers['Cache-Control']=='no-store'
    _,_,raw=request(session(),key['subscription_url'])
    links=base64.b64decode(raw).decode().splitlines()
    assert len(links)==1, 'unexpected subscription members'
    link=urlsplit(links[0]); params=parse_qs(link.query)
    assert link.scheme=='vless' and params.get('security')==['tls'] and params.get('flow')==['xtls-rprx-vision']
    # The subscription's loopback host is replaced with the same panel's Docker DNS.
    config={'log':{'loglevel':'none'},'inbounds':[{'listen':'0.0.0.0','port':1080,'protocol':'http'}],
            'outbounds':[{'protocol':'vless','settings':{'vnext':[{'address':'panel','port':link.port,'users':[{'id':link.username,'encryption':'none','flow':params['flow'][0]}]}]},
                          'streamSettings':{'network':'tcp','security':'tls','tlsSettings':{'serverName':'panel','disableSystemRoot':True,'certificates':[{'certificateFile':'/run/secrets/public_cert','usage':'verify'}]}}}]}
    write('vpn.json',json.dumps(config))
    compose('--profile','vpn','up','--no-build','--pull','never','--force-recreate','-d','vpn-client')
    assert wait_until(vpn_connected), 'VPN data plane failed'

def vpn_connected():
    proxy=urllib.request.build_opener(urllib.request.ProxyHandler({'http':'http://127.0.0.1:59448'}))
    try:
        with proxy.open('http://origin:8000/',timeout=5) as response:
            return response.read()==VPN_ORIGIN_MARKER
    except OSError:return False

def restore():
    assert PROFILE == 'legacy', 'legacy restore requires LOCAL_PROFILE=legacy'
    opener,trial,credentials=signup()
    _,_,owner=api(opener,'/api/v1/me')
    csrf=owner['csrf_token']
    # Actual cooldown and delivered proofs, kept only in process memory.
    target='restore-target-'+uuid4().hex+'@example.test'
    print('Checking restore: waiting for the real recipient cooldown',flush=True)
    time.sleep(61)
    assert api(opener,'/api/v1/me/email-change',{'current_password':credentials['password'],'new_email':target},csrf)[0]==202
    pair=[wait_until(lambda:mail_token(email,'/confirm-email-change')) for email in (credentials['email'],target)]
    assert api(opener,'/api/v1/auth/email-change/confirm',{'token':pair[0]})[2]['completed'] is False
    time.sleep(61)
    assert api(opener,'/api/v1/auth/password-reset',{'email':credentials['email'],'locale':'en'})[0]==202
    reset=wait_until(lambda:mail_token(credentials['email'],'/reset-password'))
    original=database()
    # Controlled fault pauses only our test DB after the external readback.
    sql("""CREATE FUNCTION local_pause_apply() RETURNS trigger LANGUAGE plpgsql AS $$
      BEGIN IF NEW.status='applied' THEN PERFORM pg_sleep(120); END IF; RETURN NEW; END $$;
      CREATE TRIGGER local_pause_apply BEFORE UPDATE ON trial_operations FOR EACH ROW EXECUTE FUNCTION local_pause_apply();""")
    operation=approve(trial)['operation_id']
    wait_until(lambda:sql("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='PgSleep';")=='1')
    before=snapshot(operation)
    assert before['status']=='provisioning' and before['grant']=='reserved' and before['job']=='running' and before['grants']==1
    panel_before=panel_readback(before['target'])
    # Close only cabinet ingress; native panel/SMTP routes are needed for reconcile.
    data=re.sub(r'^CABINET_MAINTENANCE=.*\n?', '', ENV.read_text(), flags=re.M)
    write('public.env',data+'CABINET_MAINTENANCE=true\n')
    compose('up','--no-build','--pull','never','--no-deps','--force-recreate','-d','gateway')
    wait_until(maintenance_ready)
    dump=compose('exec','-T','postgres','pg_dump','-U',PG_USER,'-Fc','-d',original)
    (STATE/'restore.dump').write_bytes(dump);(STATE/'restore.dump').chmod(0o600)
    # The dump contains a cookie revoked afterwards; restore must not revive it.
    sql("DELETE FROM sessions WHERE account_id=(SELECT account_id FROM trial_operations WHERE id=:'op');",operation=operation)
    # Kill the sole writer: the committed River job remains running in the dump.
    compose('kill','-s','SIGKILL','backend')
    sql("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='PgSleep' AND pid<>pg_backend_pid();")
    restored=restore_name()
    sql('CREATE DATABASE '+restored+';',db='postgres')  # Generated identifier only.
    compose('exec','-T','postgres','pg_restore','-U',PG_USER,'--no-owner','--no-privileges','-d',restored,stdin=dump)
    assert snapshot(operation,restored)==before
    sql('DROP TRIGGER local_pause_apply ON trial_operations; DROP FUNCTION local_pause_apply();',db=original)
    sql("""DROP TRIGGER local_pause_apply ON trial_operations; DROP FUNCTION local_pause_apply();
      UPDATE river_job SET attempted_at=now()-interval '4 minutes' WHERE kind='trial_provision' AND state='running';""",db=restored)
    # Advance the job timestamp only in this fixture; River performs normal rescue.
    url=(STATE/'database-url').read_text().strip()
    write('database-url',url.replace('/'+original+'?', '/'+restored+'?'))
    compose('run','--rm','--no-deps','-T','migrate')
    maintenance=(ROOT/'backend/db/maintenance/post_restore_auth.sql').read_text()
    first=sql(maintenance)
    assert len(first.splitlines())==3 and all(n.isdigit() for n in first.splitlines()), 'maintenance must return counts only'
    assert sql(maintenance)=='0\n0\n0', 'maintenance not idempotent'
    assert sql("SELECT count(*) FROM sessions;")== '0'
    assert sql("SELECT count(*) FROM credential_challenges WHERE NOT revoked AND used_at IS NULL;")== '0'
    assert sql("SELECT count(*) FROM mail_deliveries WHERE kind='credential' AND ciphertext IS NOT NULL;")== '0'
    print('PASS: closed ingress, restored sessions/proofs revoked, proof payload cleared; repeated SQL returns zero counts',flush=True)
    compose('up','--no-build','--pull','never','-d','reconcile')
    wait_until(lambda:snapshot(operation)['status']=='applied',timeout=60)
    after=snapshot(operation)
    assert after['target']==before['target'] and after['grant']=='granted' and after['grants']==1
    assert panel_readback(after['target'])==panel_before, 'restore changed external identity or limits'
    panel,_=login_panel()
    for inbound in panel_call(panel,'panel/api/inbounds/list'):
        clients=inbound['settings']['clients']
        assert sum(c['email']==before['target']['panel_key'] for c in clients)==(1 if inbound['id'] in before['target']['inbound_ids'] else 0)
    compose('stop','reconcile')
    compose('up','--no-build','--pull','never','-d','backend')
    wait_until(internal_ready)
    write('public.env',ENV.read_text().replace('CABINET_MAINTENANCE=true','CABINET_MAINTENANCE=false'))
    compose('up','--no-build','--pull','never','--no-deps','--force-recreate','-d','gateway')
    wait_until(ready)
    try:
        api(opener,'/api/v1/me')
        raise AssertionError('restored cookie accepted')
    except HTTPError as error:
        assert error.code==401, 'old session must require login'
    for path,body in [('/api/v1/auth/password-reset/complete',{'token':reset,'new_password':'Unused local password '+secrets.token_urlsafe(20)}),
                      *[('/api/v1/auth/email-change/confirm',{'token':token}) for token in pair]]:
        try:
            api(opener,path,body)
            raise AssertionError('restored proof accepted')
        except HTTPError as error:
            assert error.code==400 and json.loads(error.read())['error']['code']=='INVALID_VERIFICATION', 'old proof must fail closed'
    status,_,login=api(opener,'/api/v1/auth/login',credentials)
    assert status==200 and login['account']['account_id']==owner['account']['account_id'], 'restored login ownership'
    wait_until(lambda:active(opener))
    vpn(opener)
    print('PASS: pg_dump/restore with real panel client, reserved grant and running River job; old cookies/proofs invalid, new owner login, same target/client/grant/VPN')

def rollback():
    query="SELECT json_build_object('accounts',(SELECT count(*) FROM accounts),'operations',(SELECT count(*) FROM trial_operations),'grants',(SELECT count(*) FROM trial_grants));"
    before=sql(query)
    compose('stop','gateway')
    try:
        assert not ready(), 'ingress still available'
        assert vpn_connected(), 'existing VPN stopped with ingress'
        assert sql(query)==before, 'rollback changed owned data'
    finally:
        compose('up','--no-build','--pull','never','-d','gateway')
    wait_until(ready)
    print('PASS: bounded ingress rollback; PG data, panel and existing VPN retained')

def check():
    if PROFILE == 'native':
        native_check()
        return
    opener,trial,_=signup()
    operation=approve(trial)['operation_id']
    subscription=wait_until(lambda:active(opener))
    assert subscription['devices']==1 and subscription['traffic_limit_bytes']==15*1024**3
    panel_readback(snapshot(operation)['target'])
    vpn(opener)
    print('PASS: real TLS SMTP, public API, Python adapter decision, 3X-UI readback, subscription and VLESS/TLS VPN; Telegram transport not tested',flush=True)
    restore()
    rollback()

def native_restart():
    opener,trial,credentials=signup()
    account=sql("SELECT account_id FROM trial_requests WHERE id=:'op'::uuid;",operation=trial['request_id'])
    write('native-operator-account',str(UUID(account)))
    compose('exec','-T','backend','/server','operator','grant','--account-file','/run/secrets/native_operator_account')
    _,_,login=api(opener,'/api/v1/auth/login',credentials)
    # Hold only this owned fixture's provisioning insert, without a sleeping transaction.
    sql("""CREATE FUNCTION native_hold_provision() RETURNS trigger LANGUAGE plpgsql AS $$
      BEGIN IF NEW.kind='trial_provision' THEN NEW.state='scheduled';
        NEW.scheduled_at=clock_timestamp()+interval '1 hour'; END IF; RETURN NEW; END $$;
      CREATE TRIGGER native_hold_provision BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION native_hold_provision();""")
    try:
        status,_,decision=api(opener,'/api/v1/operator/trial-requests/'+trial['request_id']+'/decision',
                             {'decision':'approve','reason':''},login['csrf_token'],str(uuid4()))
        assert status==200, 'native web decision failed'
        operation=decision['operation_id']
        before=snapshot(operation)
        assert before['status']=='pending' and before['grant']=='reserved' and before['grants']==1 and before['job']=='scheduled'
        identity_query="SELECT vpn_id::text||':'||sub_id||':'||panel_key FROM accounts WHERE id=(SELECT account_id FROM trial_operations WHERE id=:'op');"
        identity=sql(identity_query,operation=operation)
        compose('stop','backend')
        sql('DROP TRIGGER native_hold_provision ON river_job; DROP FUNCTION native_hold_provision();')
        sql("UPDATE river_job SET state='available',scheduled_at=clock_timestamp() WHERE kind='trial_provision' AND args->>'operation_id'=:'op';",operation=operation)
        compose('up','--no-build','--pull','never','-d','backend')
        wait_until(ready)
        wait_until(lambda:snapshot(operation)['status']=='applied')
        after=snapshot(operation)
        assert after['grant']=='granted' and after['grants']==1 and sql(identity_query,operation=operation)==identity
        assert sql("SELECT count(*) FROM trial_operations WHERE request_id=(SELECT request_id FROM trial_operations WHERE id=:'op');",operation=operation)=='1'
        panel_readback(after['target'])
        assert active(opener) is not None
        print('PASS: compiled Go process stopped after commit and restarted; same operation, grant, keys and real panel readback; Telegram disabled',flush=True)
    finally:
        sql('DROP TRIGGER IF EXISTS native_hold_provision ON river_job; DROP FUNCTION IF EXISTS native_hold_provision();')
        write('native-operator-account','')
        compose('up','--no-build','--pull','never','-d','backend')

def native_check():
    running=set(compose('ps','--services','--status','running').decode().splitlines())
    assert 'backend' in running and not running.intersection({'bot','reconcile'}), 'native profile must have one application process'
    config=json.loads(compose('config','--format','json'))
    assert config['services']['backend']['environment']['TELEGRAM_ENABLED']=='false', 'automated check requires simulated Telegram'
    environment=os.environ.copy()
    if not environment.get('TEST_DATABASE_URL_FILE') or not environment.get('TEST_REDIS_URL_FILE'):
        command(['docker','compose','-f','deploy/acceptance/compose.test.yml','up','-d','--wait','postgres','redis'])
        environment['TEST_DATABASE_URL_FILE']=write('test-database-url','postgres://platform_test@127.0.0.1:55491/platform_test?sslmode=disable')
        environment['TEST_REDIS_URL_FILE']=write('test-redis-url','redis://127.0.0.1:56391/0')
    environment['NATIVE_DOCKER_STATE']=str(STATE)
    log=STATE/'native-go.log'
    with log.open('w') as output:
        log.chmod(0o600)
        result=subprocess.run(['go','test','-race','./tests','-run','TestNativeTrial','-count=1'],
                              cwd=ROOT/'backend',env=environment,stdout=output,stderr=subprocess.STDOUT,timeout=180)
    assert result.returncode==0, 'native Go integration failed; see private native-go.log'
    print('PASS: native Go HTTP/jobs/Telegram integration with real TLS SMTP and 3X-UI3.7.0; Bot API simulated',flush=True)
    native_restart()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action',choices=('up','check','restore','down'))
    parser.add_argument('--reuse-images',action='store_true')
    args=parser.parse_args()
    if args.reuse_images and args.action!='up':parser.error('--reuse-images is only for up')
    if args.action=='up':up(args.reuse_images)
    elif args.action=='check':check()
    elif args.action=='restore':restore()
    else:compose('--profile','vpn','--profile','telegram','down')

if __name__=='__main__':
    try:main()
    except Exception as error:
        # Never print raw provider responses or tracebacks containing registration/key data.
        status=' (HTTP '+str(error.code)+')' if isinstance(error,HTTPError) else ''
        print(type(error).__name__+': local acceptance step failed'+status+'; inspect private state/runbook')
        raise SystemExit(1)
