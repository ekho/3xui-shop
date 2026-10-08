import type {Reporter,TestCase,TestResult,FullResult} from '@playwright/test/reporter';
// Real-flow failure output must not include email proof, cookies or subscription URLs.
export default class SafeReporter implements Reporter{
 onTestEnd(test:TestCase,result:TestResult){process.stdout.write(test.title+': '+result.status+'\n');if(result.status!=='passed'){const checkpoints=result.stdout.map(v=>v.toString()).join('').split('\n').filter(v=>/^TG_TRIAL_CHECKPOINT:[a-z_]+(?::[0-9]{3})?$/.test(v));for(const point of checkpoints.slice(-2))process.stdout.write(point+'\n');}}
 onEnd(result:FullResult){process.stdout.write('Real browser suite: '+result.status+'\n');}
}
