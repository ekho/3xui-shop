"""Local wiring check with disposable credentials; never starts a Telegram poller.
Prerequisite: build the three local images with compose.acceptance.yml first.
"""
import base64
import http.client
import json
import os
from pathlib import Path
import secrets
import socket
import ssl
import subprocess
import tempfile
import time

ROOT=Path(__file__).resolve().parents[2]
COMPOSE=ROOT/'deploy/acceptance/compose.acceptance.yml'
REDACTIONS=[]

def command(args,**kwargs):
    result=subprocess.run(args,cwd=ROOT,capture_output=True,timeout=120,**kwargs)
    if result.returncode:
        detail=result.stderr.decode(errors='replace')[-4000:]
        for value in REDACTIONS:detail=detail.replace(value,'[redacted]')
        raise RuntimeError('local smoke command failed: '+args[0]+' '+detail)
    return result.stdout

def run():
    with tempfile.TemporaryDirectory(prefix='cabinet-smoke-') as temporary:
        folder=Path(temporary)
        # Secrets stay in mode0600 files. No command line contains their values.
        files={}
        values={'pg-password':secrets.token_urlsafe(24),'redis-url':'redis://redis:6379/0','mail-key':base64.b64encode(secrets.token_bytes(32)).decode(),'code-key':base64.b64encode(secrets.token_bytes(32)).decode(),'adapter-token':secrets.token_urlsafe(32),'smtp-password':secrets.token_urlsafe(24),'panel-token':secrets.token_urlsafe(32),'test-bot-token':'1:'+secrets.token_urlsafe(32)}
        values['database-url']='postgres://cabinet:'+values['pg-password']+'@postgres:5432/cabinet?sslmode=disable'
        REDACTIONS.extend(values.values())
        for name,value in values.items():
            path=folder/name;path.write_text(value);path.chmod(0o600);files[name]=str(path)
        cert=folder/'cert.pem';key=folder/'key.pem'
        command(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=cabinet.example.test','-addext','subjectAltName=DNS:cabinet.example.test,DNS:gateway,IP:127.0.0.1','-keyout',str(key),'-out',str(cert)])
        key.chmod(0o600)
        data=(ROOT/'deploy/acceptance/.env.example').read_text()
        mapping={'PG_PASSWORD_FILE':'pg-password','DATABASE_URL_FILE':'database-url','REDIS_URL_FILE':'redis-url','MAIL_KEY_FILE':'mail-key','CODE_KEY_FILE':'code-key','BOT_ADAPTER_TOKEN_FILE':'adapter-token','SMTP_PASSWORD_FILE':'smtp-password','PANEL_TOKEN_FILE':'panel-token','BOT_TOKEN_FILE':'test-bot-token'}
        lines=[]
        for line in data.splitlines():
            name=line.split('=',1)[0]
            if name in mapping:line=name+'='+files[mapping[name]]
            elif name.endswith('_CA_FILE') or name in ('PUBLIC_CERT_FILE','ADAPTER_CERT_FILE'):line=name+'='+str(cert)
            elif name in ('PUBLIC_KEY_FILE','ADAPTER_KEY_FILE'):line=name+'='+str(key)
            elif name=='APP_RUNTIME_UID':line=name+'='+str(os.getuid())
            elif name=='APP_RUNTIME_GID':line=name+'='+str(os.getgid())
            lines.append(line)
        envfile=folder/'public.env';envfile.write_text('\n'.join(lines)+'\n');envfile.chmod(0o600)
        compose=['docker','compose','--project-name','cabinet-smoke','--profile','restore','--env-file',str(envfile),'-f',str(COMPOSE)]
        try:
            command(compose+['config','--quiet'])
            command(compose+['up','--pull','never','--no-build','-d','backend','gateway'])
            context=ssl.create_default_context(cafile=str(cert))
            def get(path):
                conn=http.client.HTTPSConnection('cabinet.example.test',58443,context=context,timeout=3)
                # Keep the real hostname/SNI while routing this test-only connection to loopback.
                conn._create_connection=lambda address,timeout,source:socket.create_connection(('127.0.0.1',58443),timeout,source)
                try:
                    conn.request('GET',path,headers={'Host':'cabinet.example.test:58443'})
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
                'termsVersion':public_values['TERMS_VERSION'],
                'privacyVersion':public_values['PRIVACY_VERSION'],
                'termsURL':public_values['TERMS_URL'],
                'privacyURL':public_values['PRIVACY_URL'],
                'supportURL':public_values['SUPPORT_URL'],
            }, 'cabinet configuration must come from the deployment environment'
            assert get('/internal/v1/telegram/jobs/claim')[0]==404
            status,headers,_=get('/api/v1/me')
            assert status==401
            assert one_header(headers,'Referrer-Policy')=='no-referrer'
            assert "frame-ancestors 'none'" in one_header(headers,'Content-Security-Policy')
            assert one_header(headers,'X-Content-Type-Options')=='nosniff'
            assert one_header(headers,'Cache-Control')=='no-store'
            private="""import json,os,ssl,urllib.request
from pathlib import Path
ctx=ssl.create_default_context(cafile=os.environ['WEB_TRIAL_API_CA_FILE'])
url=os.environ['WEB_TRIAL_API_URL']
request=urllib.request.Request(url+'/internal/v1/telegram/jobs/claim',data=b'{"limit":1}',headers={'Content-Type':'application/json','Authorization':'Bearer '+Path(os.environ['WEB_TRIAL_API_TOKEN_FILE']).read_text().strip()})
with urllib.request.urlopen(request,context=ctx,timeout=5) as r:assert r.status==200 and json.load(r)=={'jobs':[]}
"""
            command(compose+['run','--rm','--no-deps','--entrypoint','python','bot','-c',private])
            command(compose+['run','--rm','--no-deps','migrate'])
            command(compose+['run','--rm','--no-deps','migrate'])
            # Bounded rollback closes writers' ingress and keeps PG + backend queues.
            command(compose+['stop','gateway'])
            running=command(compose+['ps','--services','--status','running']).decode().splitlines()
            assert 'backend' in running and 'postgres' in running and 'redis' in running and 'gateway' not in running
            command(compose+['stop','backend'])
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
            no_http="""import socket
try:
    connection=socket.create_connection(('reconcile',8080),timeout=2)
except ConnectionRefusedError:
    pass
else:
    connection.close()
    raise AssertionError('restore runtime opened HTTP')
"""
            command(compose+['run','--rm','--no-deps','--entrypoint','python','bot','-c',no_http])
            print('PASS: local HTTPS, runtime public config, public/private routing, secret-file access, repeated migrations, bounded rollback, provision-only restore runtime')
        except Exception:
            details=command(compose+['logs','--no-color','--tail','25','migrate','backend','gateway','reconcile']).decode(errors='replace')
            for value in REDACTIONS:details=details.replace(value,'[redacted]')
            print(details)
            raise
        finally:
            # Only this disposable project is removed; acceptance/production volumes are untouched.
            command(compose+['down','-v'])

if __name__=='__main__':run()
