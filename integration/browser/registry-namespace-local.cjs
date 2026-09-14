const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const base=process.env.REGISTRY_APP_URL||'http://127.0.0.1:19746';
const id=process.env.REGISTRY_TEST_CONNECTION;
const password=process.env.REGISTRY_TEST_PASSWORD;
const output=path.resolve(process.env.REGISTRY_TEST_OUTPUT||'.scratch/namespace-test');
if(!id||!password)throw Error('Set REGISTRY_TEST_CONNECTION and REGISTRY_TEST_PASSWORD');
(async()=>{
 const browser=await chromium.launch({channel:'msedge',headless:true});fs.mkdirSync(output,{recursive:true});
 const context=await browser.newContext({locale:'zh-CN',viewport:{width:1600,height:1000}});const p=await context.newPage(),errors=[],checks=[];p.on('pageerror',e=>errors.push(e.message));
 try{
 assert.equal((await context.request.get(base+'/resources/registries',{maxRedirects:0})).status(),303);assert.equal((await context.request.get(base+'/missing-registry-route')).status(),404);
 await p.goto(base+'/login');await p.locator('[name=username]').fill('admin');await p.locator('[name=password]').fill(password);await p.locator('form[action="/login"] button[type=submit]').click();await p.waitForURL('**/monitor');checks.push('basic access and login');
 const url=base+'/resources/registries?connection='+id;await p.goto(url);await p.locator('.registry-table').waitFor();
 assert.equal(await p.locator('.registry-connection-rail .registry-namespaces').count(),0);assert.equal(await p.locator('.registry-browser > .registry-namespaces').count(),1);
 const rail=await p.locator('.registry-connection-rail').boundingBox(),tree=await p.locator('.registry-namespaces').boundingBox(),inventory=await p.locator('.registry-inventory').boundingBox();assert.ok(tree.x>=rail.x+rail.width-1);assert.ok(inventory.x>=tree.x+tree.width-1);checks.push('separate connection, namespace and inventory columns');
 const root=p.locator('details[data-registry-namespace="jiangnan/"]');
 const main=p.locator('details[data-registry-namespace="jiangnan/main/"]');
 const rootLink=root.locator(':scope > summary > a'),mainLink=main.locator(':scope > summary > a');
 await rootLink.click();await p.waitForURL(url+'&namespace=jiangnan%2F');
 await p.locator('.registry-namespaces a[aria-current]').waitFor();assert.equal(await root.getAttribute('open'),null);assert.equal(await p.locator('.registry-image-link').count(),3);checks.push('first parent click filters without expanding');
 await rootLink.click();assert.notEqual(await root.getAttribute('open'),null);assert.equal(p.url(),url+'&namespace=jiangnan%2F');
 await mainLink.click();await p.waitForURL(url+'&namespace=jiangnan%2Fmain%2F');await p.locator('.registry-namespaces a[aria-current]').filter({hasText:/^main\/$/}).waitFor();
 assert.notEqual(await root.getAttribute('open'),null);assert.equal(await main.getAttribute('open'),null);assert.equal(await p.locator('.registry-image-link').count(),2);assert.equal(await p.locator('.registry-image-link').filter({hasText:'mainly'}).count(),0);
 await mainLink.click();assert.notEqual(await main.getAttribute('open'),null);checks.push('second click expands only selected node');
 await rootLink.click();await p.waitForURL(url+'&namespace=jiangnan%2F');await p.locator('.registry-namespaces a[aria-current]').filter({hasText:/^jiangnan\/$/}).waitFor();assert.notEqual(await root.getAttribute('open'),null);assert.notEqual(await main.getAttribute('open'),null);
 await rootLink.click();assert.equal(await root.getAttribute('open'),null);assert.notEqual(await main.getAttribute('open'),null);assert.equal(p.url(),url+'&namespace=jiangnan%2F');
 await root.locator(':scope > summary').press('Space');assert.notEqual(await root.getAttribute('open'),null);assert.notEqual(await main.getAttribute('open'),null);checks.push('parent collapse and keyboard expansion preserve child state');
 await p.reload();await p.locator('.registry-table').waitFor();assert.notEqual(await root.getAttribute('open'),null);assert.notEqual(await main.getAttribute('open'),null);checks.push('reload preserves tree state');
 await mainLink.click();await p.waitForURL(url+'&namespace=jiangnan%2Fmain%2F');await p.locator('.registry-namespaces a[aria-current]').filter({hasText:/^main\/$/}).waitFor();assert.notEqual(await main.getAttribute('open'),null);
 assert.equal(await p.locator('.registry-namespaces a').filter({hasText:/^main\/$/}).count(),1);assert.equal(await p.locator('.registry-namespace-node details > a').count(),0);checks.push('single label and sibling prefix isolation');
 await p.evaluate(()=>Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>{}))));await p.screenshot({path:path.join(output,'desktop.png'),fullPage:true,animations:'disabled'});
 await p.locator('.registry-connection-actions a').click();const panel=p.locator('.task-panel-host.is-open .task-panel');await panel.waitFor();assert.equal(await panel.locator('[name=name]').inputValue(),'江南镜像仓库');await panel.locator('[data-task-panel-close]').click();await panel.waitFor({state:'detached'});checks.push('current connection editor');
 await p.setViewportSize({width:390,height:844});await p.reload();await p.locator('.registry-table').waitFor();await p.evaluate(()=>Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>{}))));assert.ok(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await p.screenshot({path:path.join(output,'mobile.png'),fullPage:true,animations:'disabled'});checks.push('mobile layout without page overflow');
 const plainContext=await browser.newContext({javaScriptEnabled:false,storageState:await context.storageState(),viewport:{width:1280,height:900}});const plain=await plainContext.newPage();await plain.goto(url);await plain.locator('.registry-namespaces summary > a').filter({hasText:/^jiangnan\/$/}).click();await plain.waitForURL(url+'&namespace=jiangnan%2F');assert.equal(await plain.locator('.registry-image-link').count(),3);await plain.locator('.registry-namespaces summary > a').filter({hasText:/^main\/$/}).click();await plain.waitForURL(url+'&namespace=jiangnan%2Fmain%2F');assert.equal(await plain.locator('.registry-image-link').count(),2);checks.push('no-JavaScript parent and nested navigation');
 assert.deepEqual(errors,[]);fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({checks,errors,base,id},null,2));console.log(JSON.stringify({checks,errors}));
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
