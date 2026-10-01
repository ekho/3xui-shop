import {spawnSync} from 'node:child_process';
const real=process.env.S01_E2E_MODE==='real'&&!process.env.S01_TEST_ORIGIN;
const args=real?['test','-C','../backend','./tests','-run','^TestS01FlowAndFailures$','-count=1','-v']:['playwright','test',...process.argv.slice(2)];
const result=spawnSync(real?'go':'npx',args,{stdio:'inherit',env:{...process.env,...(real?{S01_RUN_BROWSER:'1'}:{})}});
process.exit(result.status??1);
