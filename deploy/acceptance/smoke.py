"""Local wiring check with disposable credentials; never starts a Telegram poller.
Prerequisite: build the two local images with compose.acceptance.yml first.
"""
import base64
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import secrets
import re
import socket
import ssl
import subprocess
import tempfile
import time
from uuid import uuid4

ROOT=Path(__file__).resolve().parents[2]
COMPOSE=ROOT/'deploy/acceptance/compose.acceptance.yml'
REDACTIONS=[]

def compose_environment(args):
    # Shell interpolation outranks --env-file; only owned fixture inputs may win.
    names=set(re.findall(r'\$\{([A-Z_]+)',COMPOSE.read_text()))
    env={name:value for name,value in os.environ.items() if name not in names and not name.startswith('COMPOSE_')}
    envfile=Path(args[args.index('--env-file')+1])
    env.update(dict(line.split('=',1) for line in envfile.read_text().splitlines() if line and not line.startswith('#')))
    return env

def command(args,**kwargs):
    if args[:2]==['docker','compose']:kwargs['env']=compose_environment(args)
    result=subprocess.run(args,cwd=ROOT,capture_output=True,timeout=120,**kwargs)
    if result.returncode:
        detail=result.stderr.decode(errors='replace')[-4000:]
        for value in REDACTIONS:detail=detail.replace(value,'[redacted]')
        raise RuntimeError('local smoke command failed: '+args[0]+' '+detail)
    return result.stdout

def run():
    with tempfile.TemporaryDirectory(prefix='cabinet-smoke-') as temporary:
        folder=Path(temporary)
        project='cabinet-smoke-'+uuid4().hex[:8]
        ids=command(['docker','network','ls','-q']).decode().split()
        networks=json.loads(command(['docker','network','inspect',*ids])) if ids else []
        used=[ipaddress.ip_network(c['Subnet']) for n in networks for c in (n.get('IPAM',{}).get('Config') or []) if c.get('Subnet')]
        subnet=next((f'172.31.{i}.0/28' for i in range(140,240) if not any(ipaddress.ip_network(f'172.31.{i}.0/28').overlaps(n) for n in used if n.version==4)),None)
        assert subnet, 'No free owned smoke subnet'
        # Secrets stay in mode0600 files. No command line contains their values.
        files={}
        values={'pg-password':secrets.token_urlsafe(24),'redis-url':'redis://redis:6379/0','mail-key':base64.b64encode(secrets.token_bytes(32)).decode(),'code-key':base64.b64encode(secrets.token_bytes(32)).decode(),'smtp-password':secrets.token_urlsafe(24),'panel-token':secrets.token_urlsafe(32)}
        values['database-url']='postgres://cabinet:'+values['pg-password']+'@postgres:5432/cabinet?sslmode=disable'
        REDACTIONS.extend(values.values())
        for name,value in values.items():
            path=folder/name;path.write_text(value);path.chmod(0o600);files[name]=str(path)
        cert=folder/'cert.pem';key=folder/'key.pem'
        command(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=cabinet.example.test','-addext','subjectAltName=DNS:cabinet.example.test,DNS:gateway,IP:127.0.0.1','-keyout',str(key),'-out',str(cert)])
        key.chmod(0o600)
        data=(ROOT/'deploy/acceptance/.env.example').read_text()
        mapping={'PG_PASSWORD_FILE':'pg-password','DATABASE_URL_FILE':'database-url','REDIS_URL_FILE':'redis-url','MAIL_KEY_FILE':'mail-key','CODE_KEY_FILE':'code-key','SMTP_PASSWORD_FILE':'smtp-password','PANEL_TOKEN_FILE':'panel-token'}
        lines=[]
        for line in data.splitlines():
            name=line.split('=',1)[0]
            if name in mapping:line=name+'='+files[mapping[name]]
            elif name.endswith('_CA_FILE') or name == 'PUBLIC_CERT_FILE':line=name+'='+str(cert)
            elif name == 'PUBLIC_KEY_FILE':line=name+'='+str(key)
            elif name=='APP_RUNTIME_UID':line=name+'='+str(os.getuid())
            elif name=='APP_RUNTIME_GID':line=name+'='+str(os.getgid())
            elif name=='APP_NETWORK_SUBNET':line=name+'='+subnet
            elif name=='APP_GATEWAY_IP':line=name+'='+str(ipaddress.ip_network(subnet).network_address+14)
            lines.append(line)
        envfile=folder/'public.env';envfile.write_text('\n'.join(lines)+'\n');envfile.chmod(0o600)
        override=folder/'ports.yml';override.write_text("services:\n  gateway:\n    ports: !override ['127.0.0.1::8443']\n")
        compose=['docker','compose','--project-name',project,'--profile','restore','--env-file',str(envfile),'-f',str(COMPOSE),'-f',str(override)]
        try:
            command(compose+['config','--quiet'])
            command(compose+['up','--pull','never','--no-build','-d','backend','gateway'])
            port=int(command(compose+['port','gateway','8443']).decode().strip().rsplit(':',1)[1])
            context=ssl.create_default_context(cafile=str(cert))
            def get(path):
                conn=http.client.HTTPSConnection('cabinet.example.test',port,context=context,timeout=3)
                # Keep the real hostname/SNI while routing this test-only connection to loopback.
                conn._create_connection=lambda address,timeout,source:socket.create_connection(('127.0.0.1',port),timeout,source)
                try:
                    conn.request('GET',path,headers={'Host':'cabinet.example.test:'+str(port)})
                    response=conn.getresponse();return response.status,response.getheaders(),response.read()
                finally:conn.close()
            def one_header(headers,name):
                values=[value for key,value in headers if key.lower()==name.lower()]
                assert len(values)==1, name+' must occur exactly once'
                return values[0]
            deadline=time.monotonic()+10
            last="not connected"
            while True:
                try:
                    status=get('/healthz')[0]
                    last='HTTP '+str(status)
                    if status==200:break
                except (OSError,http.client.HTTPException) as error:last=type(error).__name__+": "+str(error)
                if time.monotonic()>deadline:raise RuntimeError('HTTPS health readiness failed: '+last)
                time.sleep(.2)
            status,headers,body=get('/cabinet')
            assert status==200 and b'__emailToken' in body
            assert one_header(headers,'Referrer-Policy')=='no-referrer'
            assert "frame-ancestors 'none'" in one_header(headers,'Content-Security-Policy')
            assert one_header(headers,'X-Content-Type-Options')=='nosniff'
            assert one_header(headers,'Cache-Control')=='no-store'
            # Same image, dedicated Mini HTML/CSP; ordinary web/admin cannot be framed.
            for path in ('/mini-app', '/mini-app/cabinet/history'):
                mini_status, mini_headers, mini_body = get(path)
                assert mini_status == 200 and b'__emailToken' not in mini_body
                assert b'https://telegram.org' in mini_body
                policy = one_header(mini_headers, 'Content-Security-Policy')
                assert "script-src 'self' https://telegram.org;" in policy
                assert 'frame-ancestors https://web.telegram.org' in policy
                assert 'unsafe-inline' not in policy and '*' not in policy
                assert one_header(mini_headers, 'Cache-Control') == 'no-store'
                assert one_header(mini_headers, 'Referrer-Policy') == 'no-referrer'
            assert "frame-ancestors 'none'" in one_header(get('/admin/clients')[1], 'Content-Security-Policy')
            status,headers,body=get('/config.json')
            assert status==200
            assert one_header(headers,'Content-Type').startswith('application/json')
            assert one_header(headers,'Cache-Control')=='no-store'
            public_values=dict(line.split('=',1) for line in lines if '=' in line)
            assert json.loads(body)=={
                'productName':public_values['PRODUCT_NAME'],
                'termsVersion':public_values['TERMS_VERSION'],
                'privacyVersion':public_values['PRIVACY_VERSION'],
                'termsURL':public_values['TERMS_URL'],
                'privacyURL':public_values['PRIVACY_URL'],
                'supportURL':public_values['SUPPORT_URL'],
            }, 'cabinet configuration must come from the deployment environment'
            def browser_config(name):
                command(['node','web/tests/runtime-deployment.mjs'],env={**os.environ,
                    'TEST_ORIGIN':'https://cabinet.example.test:'+str(port),'EXPECTED_PRODUCT_NAME':name})
            browser_config(public_values['PRODUCT_NAME'])
            image=command(['docker','inspect','--format','{{.Image}}',command(compose+['ps','-q','gateway']).decode().strip()])
            html=get('/cabinet')[2]
            assets={p.decode():hashlib.sha256(get(p.decode())[2]).digest() for p in re.findall(rb'"(/assets/[^\"]+)"',html)}
            assert assets, 'Built web assets missing'
            replacement={**public_values,'PRODUCT_NAME':'Second deployment','TERMS_VERSION':'next','PRIVACY_VERSION':'next',
                         'TERMS_URL':'https://second.example.test/terms','PRIVACY_URL':'https://second.example.test/privacy','SUPPORT_URL':'mailto:help@second.example.test'}
            envfile.write_text('\n'.join(name+'='+value for name,value in replacement.items())+'\n')
            command(compose+['up','--pull','never','--no-build','--no-deps','-d','backend','gateway'])
            port=int(command(compose+['port','gateway','8443']).decode().strip().rsplit(':',1)[1])
            deadline=time.monotonic()+20
            while True:
                try:
                    changed=get('/config.json')
                    if get('/readyz')[0]==200 and changed[0]==200:break
                except (OSError,http.client.HTTPException):pass
                assert time.monotonic()<deadline, 'Recreated runtime did not become ready'
                time.sleep(.2)
            assert json.loads(changed[2])=={'productName':replacement['PRODUCT_NAME'],'termsVersion':'next','privacyVersion':'next',
                'termsURL':replacement['TERMS_URL'],'privacyURL':replacement['PRIVACY_URL'],'supportURL':replacement['SUPPORT_URL']}
            assert one_header(changed[1],'Cache-Control')=='no-store'
            assert image==command(['docker','inspect','--format','{{.Image}}',command(compose+['ps','-q','gateway']).decode().strip()])
            assert get('/cabinet')[2]==html and all(hashlib.sha256(get(p)[2]).digest()==digest for p,digest in assets.items())
            browser_config(replacement['PRODUCT_NAME'])
            print('PASS: two public deployments, same web image/HTML/assets, no-store and ready HTTP/River; Telegram off without token',flush=True)
            denied=subprocess.run(compose+['run','--rm','--no-deps','backend','serve'],
                                  cwd=ROOT,env=compose_environment(compose),capture_output=True,timeout=30)
            assert denied.returncode==1 and b'SERVICE_UNAVAILABLE' in denied.stderr, 'Second serve did not refuse startup'
            assert all(value.encode() not in denied.stdout+denied.stderr for value in values.values()), 'Startup error disclosed fixture credentials'
            assert get('/readyz')[0]==200, 'Denied second runtime affected the original process'
            print('PASS: second real serve refuses startup and leaves original runtime ready',flush=True)
            assert get('/internal/v1/telegram/jobs/claim')[0]==404
            status,headers,_=get('/api/v1/me')
            assert status==401
            assert one_header(headers,'Referrer-Policy')=='no-referrer'
            assert "frame-ancestors 'none'" in one_header(headers,'Content-Security-Policy')
            assert one_header(headers,'X-Content-Type-Options')=='nosniff'
            assert one_header(headers,'Cache-Control')=='no-store'
            for legacy_path in ('/internal/v1/trial-requests/unknown/decision', '/internal/v1/trial-requests/unknown/reconsider',
                                '/internal/v1/trial-operations/unknown/reconcile', '/internal/v1/telegram/jobs/claim',
                                '/internal/v1/telegram/jobs/unknown/result'):
                assert get(legacy_path)[0] == 404
            command(compose+['exec','-T','gateway','wget','-qO-','http://backend:8080/readyz'])
            command(compose+['run','--rm','--no-deps','migrate'])
            command(compose+['run','--rm','--no-deps','migrate'])
            replacement['CABINET_MAINTENANCE']='true'
            envfile.write_text('\n'.join(name+'='+value for name,value in replacement.items())+'\n')
            command(compose+['up','--pull','never','--no-build','--no-deps','-d','gateway'])
            port=int(command(compose+['port','gateway','8443']).decode().strip().rsplit(':',1)[1])
            assert get('/cabinet')[0]==503, 'gateway maintenance left admission open'
            for provider in ('yoomoney','yookassa','cryptomus','heleket'):
                for path in ('/'+provider, '/webhooks/'+provider):
                    assert get(path)[0]==405, 'gateway maintenance blocked provider callback route'
            assert get('/internal/v1/telegram/jobs/claim')[0]==404
            print('PASS: gateway maintenance closes cabinet and keeps eight provider routes with their method/auth boundary',flush=True)
            # Bounded rollback closes writers' ingress and keeps PG + backend queues.
            command(compose+['stop','gateway'])
            running=command(compose+['ps','--services','--status','running']).decode().splitlines()
            assert 'backend' in running and 'postgres' in running and 'redis' in running and 'gateway' not in running
            command(compose+['stop','backend'])
            denied=subprocess.run(compose+['run','--rm','--no-deps','-e','TELEGRAM_ENABLED=true','-e','BOT_TOKEN_FILE=',
                                          'backend','serve'],cwd=ROOT,env=compose_environment(compose),capture_output=True,timeout=30)
            assert denied.returncode==1 and b'SERVICE_UNAVAILABLE' in denied.stderr, 'Telegram-on without token did not refuse startup'
            assert all(value.encode() not in denied.stdout+denied.stderr for value in values.values()), 'Startup error disclosed fixture credentials'
            def sql(query):
                return command(compose+['exec','-T','postgres','psql','-U','cabinet','-d','cabinet','-At','-v','ON_ERROR_STOP=1','-c',query]).decode().strip()
            # A nonexistent operation finishes without touching a panel; mail must stay untouched.
            sql("INSERT INTO river_job(kind,args,queue,state) VALUES ('trial_provision','{\"operation_id\":\"11111111-1111-4111-8111-111111111111\"}','provision','available'),('mail_delivery','{\"delivery_id\":\"22222222-2222-4222-8222-222222222222\"}','default','available')")
            sql("UPDATE river_job SET state='running',attempt=1,attempted_at=now()-interval '4 minutes' WHERE kind='trial_provision'")
            command(compose+['up','--pull','never','--no-build','-d','reconcile'])
            deadline=time.monotonic()+30
            while sql("SELECT state FROM river_job WHERE kind='trial_provision'")!='completed':
                assert 'reconcile' in command(compose+['ps','--services','--status','running']).decode().splitlines(), 'restore runtime stopped'
                assert time.monotonic()<deadline, 'restore runtime did not process provision queue'
                time.sleep(.2)
            assert sql("SELECT state||':'||attempt FROM river_job WHERE kind='mail_delivery'")=='available:0', 'restore runtime processed mail'
            no_http = subprocess.run(compose+['run','--rm','--no-deps','--entrypoint','wget','gateway',
                                               '-qO-','-T','2','http://reconcile:8080/healthz'],
                                     cwd=ROOT, env=compose_environment(compose), capture_output=True, timeout=30)
            assert no_http.returncode != 0, 'restore runtime opened HTTP'
            print('PASS: local HTTPS, runtime public config, retired-adapter routing, secret-file access, repeated migrations, bounded rollback, provision-only restore runtime')
        except Exception:
            details=command(compose+['logs','--no-color','--tail','25','migrate','backend','gateway','reconcile']).decode(errors='replace')
            for value in REDACTIONS:details=details.replace(value,'[redacted]')
            print(details)
            raise
        finally:
            # Only this disposable project is removed; acceptance/production volumes are untouched.
            command(compose+['down','-v'])

if __name__=='__main__':run()
