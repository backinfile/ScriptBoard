const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");
const snapshot = version => '<!doctype html><html><body><main data-kubernetes-page data-kubernetes-refresh-failed="Refresh failed; previous data preserved" data-kubernetes-refreshed="Updated"><div class="kubernetes-refresh-toolbar"><button type="button" data-kubernetes-refresh aria-label="Refresh resources">Refresh</button><span data-kubernetes-refresh-status role="status"></span></div><div class="kubernetes-monitor-summary">cluster '+version+'</div><div data-kubernetes-alert-slot>cluster alert '+version+'</div><button data-kubernetes-expand-all>Expand all</button><button data-kubernetes-collapse-all>Collapse all</button>'+["external","workloads","nodes"].map(id=>'<details data-kubernetes-section="'+id+'"><summary>'+id+'<span data-kubernetes-section-facts="'+id+'">'+version+'</span></summary><div>'+(id==="external"?'<div data-kubernetes-external-body>service '+version+'</div>':id==="workloads"?'<div class="kubernetes-workload-controls">filters '+version+'</div><div class="kubernetes-table-shell">workload '+version+'</div>':'<div data-kubernetes-node-list>node '+version+'</div>')+'</div></details>').join("")+'</main></body></html>';
(async()=>{
 const browser=await chromium.launch({headless:true});
 try {
  const page=await browser.newPage(); let pending;let requests=0;let navigations=0;
  page.on("framenavigated",()=>navigations++);
  await page.route("http://kubernetes.test/**",async route=>{
   if(!new URL(route.request().url()).searchParams.has("refresh"))return route.fulfill({contentType:"text/html",body:snapshot(1)});
   requests++;pending=route;
  });
  await page.goto("http://kubernetes.test/?cluster=demo&namespace=production&query=api");
  await page.addScriptTag({path:path.resolve(__dirname,"../../internal/web/ui/assets/app.js")});
  assert.equal(await page.locator("details[open]").count(),0);
  await page.locator('[data-kubernetes-section="external"] summary').click();
  const url=page.url();const history=await page.evaluate(()=>window.history.length);
  const button=page.locator("[data-kubernetes-refresh]").first();
  await button.click();
  await page.waitForFunction(()=>document.querySelectorAll("[data-kubernetes-refresh]:disabled").length===1);
  assert.equal(requests,1);
  assert.equal(new URL(pending.request().url()).searchParams.get("namespace"),"production");
  assert.equal(new URL(pending.request().url()).searchParams.get("query"),"api");
  await pending.fulfill({contentType:"text/html",body:snapshot(2)});
  await page.waitForFunction(()=>document.querySelector("[data-kubernetes-external-body]").textContent==="service 2");
  assert.equal(await page.locator(".kubernetes-table-shell").textContent(),"workload 2");
  assert.equal(await page.locator("[data-kubernetes-node-list]").textContent(),"node 2");
  assert.equal(await page.locator(".kubernetes-monitor-summary").textContent(),"cluster 1");
  assert.equal(await page.locator("[data-kubernetes-alert-slot]").textContent(),"cluster alert 1");
  assert.equal(await page.locator("details[open]").count(),1);
  assert.equal(await button.isEnabled(),true);
  assert.equal(page.url(),url);assert.equal(await page.evaluate(()=>window.history.length),history);
  assert.equal(navigations,1);
  // The shared control remains available regardless of which sections are open.
  for(const [id,version] of [["workloads",3],["nodes",4]]){
   await page.locator('[data-kubernetes-section="'+id+'"] summary').click();
   await button.click();
   await page.waitForFunction(()=>document.querySelector("[data-kubernetes-refresh]").disabled);
   await pending.fulfill({contentType:"text/html",body:snapshot(version)});
   await page.waitForFunction(v=>document.querySelector("[data-kubernetes-external-body]").textContent==="service "+v,version);
   assert.equal(await page.locator("[data-kubernetes-node-list]").textContent(),"node "+version);
  }
  for(const reply of [{status:503,body:"Unavailable"},{contentType:"text/html",body:snapshot(99).replace('data-kubernetes-node-list','data-missing-node-list')}]){
   await button.click();await page.waitForFunction(()=>document.querySelector("[data-kubernetes-refresh]").disabled);
   await pending.fulfill(reply);
   await page.waitForFunction(()=>document.querySelector("[data-kubernetes-refresh-status]").textContent.startsWith("Refresh failed"));
   assert.equal(await page.locator("[data-kubernetes-external-body]").textContent(),"service 4");
   assert.equal(await button.isEnabled(),true);
  }
  await button.click();await page.waitForFunction(()=>document.querySelector("[data-kubernetes-refresh]").disabled);
  await pending.fulfill({contentType:"text/html",body:snapshot(5)});
  await page.waitForFunction(()=>document.querySelector("[data-kubernetes-external-body]").textContent==="service 5");
  await page.locator("[data-kubernetes-collapse-all]").click();assert.equal(await page.locator("details[open]").count(),0);
  await page.locator("[data-kubernetes-expand-all]").click();assert.equal(await page.locator("details[open]").count(),3);
  await page.locator("summary").first().focus();await page.keyboard.press("Space");assert.equal(await page.locator("details[open]").count(),2);
  console.log("PASS Kubernetes disclosures: shared scoped refresh control, busy state, preserved disclosures/URL/summary, atomic failure and retry, bulk toggles, keyboard.");
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
