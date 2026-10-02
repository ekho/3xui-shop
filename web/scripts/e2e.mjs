import {spawnSync} from 'node:child_process';
const real=process.env.E2E_MODE==='real'&&!process.env.TEST_ORIGIN;
const args=real?['test','-C','../backend','./tests','-run','^TestWebTrialFlowAndFailures$','-count=1','-v']:['playwright','test',...process.argv.slice(2)];
const result=spawnSync(real?'go':'npx',args,{stdio:'inherit',env:{...process.env,...(real?{RUN_BROWSER_TESTS:'1'}:{})}});
process.exit(result.status??1);
