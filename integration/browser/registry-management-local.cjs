// Run against an isolated local deployment and a disposable Registry containing
// team-a/api:v1, team-a/api:latest, team-a/jobs/worker:dev-1, team-ab/api:stable.
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const base = process.env.REGISTRY_APP_URL || 'http://127.0.0.1:19743';
const registry = process.env.REGISTRY_TEST_URL || 'http://127.0.0.1:18944';
const output = path.resolve(process.env.REGISTRY_TEST_OUTPUT || '.scratch/registry-management-local');
const password = process.env.REGISTRY_TEST_PASSWORD;
if (!password) throw Error('Set REGISTRY_TEST_PASSWORD for the isolated test deployment');
(async()=>{
 fs.mkdirSync(output,{recursive:true});
 const browser=await chromium.launch({channel:'msedge',headless:true});
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1440,height:1000}});
 const page=await context.newPage();const failures=[];page.on('pageerror',e=>failures.push(e.message));
 const checks=[];
 try {
  const anonymous=await context.request.get(base+'/resources/registries',{maxRedirects:0});assert.equal(anonymous.status(),303);checks.push('unauthenticated access redirects');
  assert.equal((await context.request.get(base+'/missing-registry-route')).status(),404);
  await page.goto(base+'/login');await page.locator('input[name=username]').fill('admin');await page.locator('input[name=password]').fill(password);await page.locator('form[action="/login"] button[type=submit]').click();await page.waitForURL('**/monitor');
  await page.goto(base+'/resources/registries');await page.getByRole('link',{name:'添加仓库',exact:true}).first().click();
  const panel=page.locator('.task-panel-host.is-open .task-panel');await panel.waitFor();assert.match(await panel.innerText(),/仓库连接/);checks.push('connection opens in shared drawer');
  await panel.locator('[name=name]').fill('本地生产仓库');await panel.locator('[name=endpoint]').fill(registry);await panel.getByRole('button',{name:'保存连接'}).click();await page.locator('.registry-image-link').first().waitFor();await page.locator('.task-panel-host.is-open .task-panel').waitFor({state:'detached'});
  const selectedURL=page.url();const id=new URL(selectedURL).searchParams.get('connection');
  await page.getByRole('link',{name:'添加仓库',exact:true}).first().click();await panel.locator('[name=name]').fill('开发仓库');await panel.locator('[name=endpoint]').fill(registry);await panel.getByRole('button',{name:'保存连接'}).click();await panel.waitFor({state:'detached'});await page.locator('.registry-connection[href='+JSON.stringify(new URL(selectedURL).pathname+new URL(selectedURL).search)+']').click();await page.waitForURL(selectedURL);checks.push('connection switching');
  await page.screenshot({path:path.join(output,'desktop.png'),fullPage:true});
  await page.locator('.registry-namespaces a').filter({hasText:/^team-a\/$/}).click();await page.waitForURL('**/*namespace=team-a%2F');assert.equal(await page.locator('.registry-image-link').count(),2);assert.equal(await page.locator('.registry-image-link').filter({hasText:'team-ab/api'}).count(),0);checks.push('namespace excludes sibling prefix');
  await page.locator('.registry-image-link').filter({hasText:/^team-a\/api$/}).click();await panel.waitFor();assert.match(await panel.innerText(),/latest/);assert.match(await panel.innerText(),/sha256:/);checks.push('real manifest details');
  await panel.getByRole('button',{name:'删除预览',exact:true}).first().click();await panel.locator('[name=confirmation]').waitFor();assert.match(await panel.innerText(),/latest, v1/);assert.ok(page.url().includes('/resources/registries?'));checks.push('detail to preview stays in drawer and shows shared tags');
  await page.screenshot({path:path.join(output,'deletion-drawer.png'),fullPage:true});
  await panel.locator('[data-task-panel-close]').click();await panel.waitFor({state:'detached'});
  await page.getByRole('link',{name:'按前缀清理',exact:true}).click();await panel.locator('[name=prefix]').fill('team-a/');await panel.locator('[name=protect]').uncheck();await panel.getByRole('button',{name:'预览清理范围',exact:true}).click();await panel.locator('[name=confirmation]').waitFor();assert.match(await panel.innerText(),/team-a\/jobs\/worker/);assert.doesNotMatch(await panel.innerText(),/team-ab\/api/);
  await panel.locator('[name=confirmation]').fill('本地生产仓库');await panel.getByRole('button',{name:'确认执行删除',exact:true}).click();await panel.getByRole('link',{name:'返回镜像仓库',exact:true}).waitFor();assert.equal(await panel.getByText('已删除引用',{exact:true}).count(),2);checks.push('real namespace manifest deletion');
  for(const repo of ['team-a/api','team-a/jobs/worker']){const result=await context.request.get(registry+'/v2/'+repo+'/tags/list');assert.equal((await result.json()).tags,null)}
  const sibling=await context.request.get(registry+'/v2/team-ab/api/tags/list');assert.deepEqual((await sibling.json()).tags,['stable']);checks.push('Registry API confirms sibling retained');
  await panel.getByRole('link',{name:'返回镜像仓库',exact:true}).click();await panel.waitFor({state:'detached'});await page.getByRole('link',{name:'操作记录',exact:true}).click();await page.locator('.registry-records details').waitFor();checks.push('persistent operation history');
  await page.setViewportSize({width:390,height:844});await page.goto(base+'/resources/registries?connection='+id);await page.locator('.registry-table').waitFor();await page.evaluate(()=>Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>{}))));await page.screenshot({path:path.join(output,'mobile.png'),fullPage:true,animations:'disabled'});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'mobile page overflow');checks.push('mobile has no page overflow');
  assert.deepEqual(failures,[]);fs.writeFileSync(path.join(output,'browser-results.json'),JSON.stringify({checks,failures,connection:id,base,registry},null,2));console.log(JSON.stringify({checks,failures,connection:id}));
 } finally {await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
