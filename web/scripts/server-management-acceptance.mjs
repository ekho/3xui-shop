import assert from 'node:assert/strict';
import fs from 'node:fs';
import {chromium} from 'playwright';

const input = JSON.parse(fs.readFileSync(process.env.SERVER_MANAGEMENT_BROWSER_INPUT, 'utf8'));
const browser = await chromium.launch();
try {
  const context = await browser.newContext({ignoreHTTPSErrors: true, baseURL: input.origin});
  await context.addCookies([input.cookie]);
  const page = await context.newPage();
  const writes = [];
  page.on('request', request => {
    if (request.url().includes('/api/v1/operator/servers') && request.method() === 'POST') {
      writes.push({url: request.url(), body: request.postDataJSON(), key: request.headers()['idempotency-key']});
    }
  });
  page.on('response', async response => {
    if (response.url().endsWith('/api/v1/operator/servers') && response.request().method() === 'GET') {
      const result = await response.json().catch(() => ({}));
      console.log('browser: target host', result.servers?.find(server => server.id === input.target)?.host);
    }
    if (response.url().endsWith(`/servers/${encodeURIComponent(input.target)}/ping`)) {
      const result = await response.json().catch(() => ({}));
      console.log('browser: ping HTTP', response.status(), result.online, result.can_delete);
    }
  });
  await page.goto('/admin/servers?lang=en');
  console.log('browser: loaded servers route');
  const target = page.getByRole('button', {name: `Open server ${input.name}`, exact: true});
  await target.focus();
  console.log('browser: focused target');
  await page.keyboard.press('Enter');
  await page.getByRole('region', {name: 'Server details', exact: true}).waitFor();
  console.log('browser: opened target');
  await page.getByRole('button', {name: 'Ping server', exact: true}).click();
  await page.getByRole('status').getByText('Server status updated.', {exact: true}).waitFor();
  console.log('browser: pinged target');
  assert(writes.some(write => write.url.endsWith(`/servers/${encodeURIComponent(input.target)}/ping`) && write.key), 'browser ping did not reach real HTTP handler');
  await page.getByRole('button', {name: 'Delete server', exact: true}).click();
  console.log('browser: delete confirmation visible');
  assert.equal(writes.filter(write => write.url.endsWith('/delete')).length, 0, 'delete sent before confirmation');
  fs.writeFileSync(input.ready, 'confirmation visible');
  const deadline = Date.now() + 15000;
  while (!fs.existsSync(input.resume) && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 25));
  assert(fs.existsSync(input.resume), 'concurrent assignment fixture did not resume browser');
  await page.getByRole('button', {name: 'Confirm server deletion', exact: true}).click();
  await page.getByRole('alert').getByText('Server is still in use and cannot be deleted.').waitFor();
  assert(writes.some(write => write.url.endsWith(`/servers/${encodeURIComponent(input.target)}/delete`) && write.body?.confirmation === true), 'confirmed delete did not use real HTTP handler');
  await page.getByLabel('Server name', {exact: true}).fill('Browser server management');
  await page.getByLabel('HTTPS host', {exact: true}).fill('https://localhost:1');
  await page.getByLabel('Maximum clients', {exact: true}).fill('0');
  const add = page.getByRole('button', {name: 'Add server', exact: true});
  await add.focus();
  await page.keyboard.press('Enter');
  await page.getByRole('status').getByText('Server added.').waitFor();
  assert(writes.some(write => write.url.endsWith('/api/v1/operator/servers') && write.body?.name === 'Browser server management' && write.body?.max_clients === 0 && write.key), 'browser add did not reach real HTTP handler');
  await page.goto('/admin/servers?lang=ru');
  await page.getByLabel('Название сервера', {exact: true}).waitFor();
  await page.getByRole('button', {name: 'Открыть сервер Browser server management', exact: true}).waitFor();
  await context.close();
  console.log('PASS: real compiled server and browser EN keyboard, add/ping/confirmed 409, RU labels');
} finally {
  await browser.close();
}
