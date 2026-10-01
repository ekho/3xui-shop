"""Local wiring check with disposable credentials; never starts a Telegram poller.
Prerequisite: build the three local images with compose.acceptance.yml first.
"""
import base64
import http.client
import os
from pathlib import Path
import secrets
import socket
import ssl
import subprocess
import tempfile
import time

ROOT=Path(__file__).resolve().parents[2]
COMPOSE=ROOT/'deploy/s01/compose.acceptance.yml'
REDACTIONS=[]

def command(args,**kwargs):
    result=subprocess.run(args,cwd=ROOT,capture_output=True,timeout=120,**kwargs)
    if result.returncode:
        detail=result.stderr.decode(errors='replace')[-4000:]
        for value in REDACTIONS:detail=detail.replace(value,'[redacted]')
        raise RuntimeError('local smoke command failed: '+args[0]+' '+detail)
    return result.stdout

def run():
    with tempfile.TemporaryDirectory(prefix='s01-smoke-') as temporary:
        folder=Path(temporary)
        # Secrets stay in mode0600 files. No command line contains their values.
        files={}
        values={'pg-password':secrets.token_urlsafe(24),'redis-url':'redis://redis:6379/0','mail-key':base64.b64encode(secrets.token_bytes(32)).decode(),'code-key':base64.b64encode(secrets.token_bytes(32)).decode(),'adapter-token':secrets.token_urlsafe(32),'smtp-password':secrets.token_urlsafe(24),'panel-token':secrets.token_urlsafe(32),'test-bot-token':'1:'+secrets.token_urlsafe(32)}
        values['database-url']='postgres://cabinet_s01:'+values['pg-password']+'@postgres:5432/cabinet_s01?sslmode=disable'
        REDACTIONS.extend(values.values())
        for name,value in values.items():
            path=folder/name;path.write_text(value);path.chmod(0o600);files[name]=str(path)
        cert=folder/'cert.pem';key=folder/'key.pem'
        command(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=cabinet.example.test','-addext','subjectAltName=DNS:cabinet.example.test,DNS:gateway,IP:127.0.0.1','-keyout',str(key),'-out',str(cert)])
        key.chmod(0o600)
        data=(ROOT/'deploy/s01/.env.example').read_text()
        mapping={'PG_PASSWORD_FILE':'pg-password','DATABASE_URL_FILE':'database-url','REDIS_URL_FILE':'redis-url','MAIL_KEY_FILE':'mail-key','CODE_KEY_FILE':'code-key','BOT_ADAPTER_TOKEN_FILE':'adapter-token','SMTP_PASSWORD_FILE':'smtp-password','PANEL_TOKEN_FILE':'panel-token','BOT_TOKEN_FILE':'test-bot-token'}
        lines=[]
        for line in data.splitlines():
            name=line.split('=',1)[0]
            if name in mapping:line=name+'='+files[mapping[name]]
            elif name.endswith('_CA_FILE') or name in ('PUBLIC_CERT_FILE','ADAPTER_CERT_FILE'):line=name+'='+str(cert)
            elif name in ('PUBLIC_KEY_FILE','ADAPTER_KEY_FILE'):line=name+'='+str(key)
            elif name=='S01_RUNTIME_UID':line=name+'='+str(os.getuid())
            elif name=='S01_RUNTIME_GID':line=name+'='+str(os.getgid())
            lines.append(line)
        envfile=folder/'public.env';envfile.write_text('\n'.join(lines)+'\n');envfile.chmod(0o600)
        compose=['docker','compose','--project-name','cabinet-s01-smoke','--env-file',str(envfile),'-f',str(COMPOSE)]
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
                    response=conn.getresponse();return response.status,dict(response.getheaders()),response.read()
                finally:conn.close()
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
            assert headers.get('Referrer-Policy')=='no-referrer'
            assert "frame-ancestors 'none'" in headers.get('Content-Security-Policy','')
            assert get('/internal/v1/telegram/jobs/claim')[0]==404
            assert get('/api/v1/me')[0]==401
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
            print('PASS: local HTTPS, public/private routing, secret-file access, repeated migrations, bounded rollback')
        except Exception:
            details=command(compose+['logs','--no-color','--tail','25','backend','gateway']).decode(errors='replace')
            for value in REDACTIONS:details=details.replace(value,'[redacted]')
            print(details)
            raise
        finally:
            # Only this disposable project is removed; acceptance/production volumes are untouched.
            command(compose+['down','-v'])

if __name__=='__main__':run()
