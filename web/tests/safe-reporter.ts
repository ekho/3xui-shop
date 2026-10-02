import type {Reporter,TestCase,TestResult,FullResult} from '@playwright/test/reporter';
// Real-flow failure output must not include email proof, cookies or subscription URLs.
export default class SafeReporter implements Reporter{
 onTestEnd(test:TestCase,result:TestResult){process.stdout.write(test.title+': '+result.status+'\n');}
 onEnd(result:FullResult){process.stdout.write('Real browser suite: '+result.status+'\n');}
}
