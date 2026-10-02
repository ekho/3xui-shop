import {strict as assert} from 'node:assert';
import {spawnSync} from 'node:child_process';
import {mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

const directory=mkdtempSync(join(tmpdir(),'web-public-config-'));
try{
 const base={TERMS_VERSION:'v1',PRIVACY_VERSION:'p1',TERMS_URL:'https://legal.example.test/terms',PRIVACY_URL:'https://legal.example.test/privacy',SUPPORT_URL:'mailto:help@example.test'};
 for(const config of [base,{...base,TERMS_VERSION:'v2 "\n </script>',TERMS_URL:'https://other.example.test/terms',SUPPORT_URL:'https://other.example.test/support'}]){
  const result=spawnSync('sh',['scripts/runtime-config.sh','true'],{env:{PATH:process.env.PATH,WEB_PUBLIC_CONFIG_DIR:directory,...config},encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
  const written=JSON.parse(readFileSync(join(directory,'config.json'),'utf8'));
  assert.deepEqual(written,{termsVersion:config.TERMS_VERSION,privacyVersion:config.PRIVACY_VERSION,termsURL:config.TERMS_URL,privacyURL:config.PRIVACY_URL,supportURL:config.SUPPORT_URL});
 }
 const oversized=spawnSync('sh',['scripts/runtime-config.sh','true'],{env:{PATH:process.env.PATH,WEB_PUBLIC_CONFIG_DIR:directory,...base,TERMS_VERSION:'x'.repeat(129)},encoding:'utf8'});
 assert.equal(oversized.status,0,oversized.stderr);
 assert.deepEqual(JSON.parse(readFileSync(join(directory,'config.json'),'utf8')),{});
}finally{rmSync(directory,{recursive:true,force:true});}
