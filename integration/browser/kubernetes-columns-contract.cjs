const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");
const fields = ["name", "namespace", "status", "ready", "node", "cpu", "memory", "restarts", "image", "started", "actions"];
const snapshot = () => `<!doctype html><html><body><main class="workspace kubernetes-page" data-kubernetes-page>
<details data-kubernetes-columns hidden><summary>Columns</summary>${fields.filter(f=>f!=="actions").map(f=>`<label><input type="checkbox" data-kubernetes-column-toggle="${f}" ${f==="name"?"disabled":""}>${f}</label>`).join("")}<button data-kubernetes-columns-reset>Reset</button></details>
<div class="kubernetes-workload-controls"><a class="kubernetes-sort-link" href="?sort=name">Sort</a></div><span data-kubernetes-section-facts="workloads">1</span>
<div class="kubernetes-table-shell"><table class="kubernetes-table"><thead><tr>${fields.map(f=>`<th data-kubernetes-column="${f}">${f}</th>`).join("")}</tr></thead><tbody><tr>${fields.map(f=>`<td data-kubernetes-column="${f}">${f==="image"?"registry.example/api:v2":f==="actions"?'<details class="action-menu"><summary>Actions</summary><div>Logs</div></details>':f}</td>`).join("")}</tr></tbody></table></div></main></body></html>`;
(async () => {
 const browser = await chromium.launch({ headless: true });
 try {
  const page = await browser.newPage({viewport:{width:1280,height:720}});
  await page.route("http://kubernetes.test/**", route=>route.fulfill({contentType:"text/html",body:snapshot()}));
  const init = async()=>{ await page.addStyleTag({path:path.resolve(__dirname,"../../internal/web/ui/assets/app.css")}); await page.addScriptTag({path:path.resolve(__dirname,"../../internal/web/ui/assets/app.js")}); };
  await page.goto("http://kubernetes.test/monitor/kubernetes"); await init();
  await page.locator("[data-kubernetes-columns] summary").click();
  await page.locator('[data-kubernetes-column-toggle="image"]').uncheck();
  assert.equal(await page.locator('td[data-kubernetes-column="image"]').isVisible(), false);
  await page.reload(); await init();
  assert.equal(await page.locator('td[data-kubernetes-column="image"]').isVisible(), false);
  await page.locator(".kubernetes-sort-link").click();
  await page.waitForFunction(()=>new URL(location.href).searchParams.get("sort")==="name");
  assert.equal(await page.locator('td[data-kubernetes-column="image"]').isVisible(), false);
  await page.setViewportSize({width:390,height:720});
  await page.locator("[data-kubernetes-columns] summary").click();
  await page.locator("[data-kubernetes-columns-reset]").click();
  assert.equal(await page.locator('td[data-kubernetes-column="image"]').isVisible(), false);
  await page.locator('[data-kubernetes-column-toggle="image"]').check();
  await page.locator('[data-kubernetes-column-toggle="started"]').check();
  assert.equal(await page.locator('td[data-kubernetes-column="started"]').isVisible(), true);
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.keyboard.press("Escape");assert.equal(await page.locator("[data-kubernetes-columns]").getAttribute("open"), null);
  await page.evaluate(()=>localStorage.setItem("scriptboard.kubernetes.columns.v1", "invalid JSON"));
  await page.reload();await init();assert.equal(await page.locator('td[data-kubernetes-column="name"]').isVisible(),true);
  console.log("PASS columns: persistence, partial sorting, reset, narrow viewport, keyboard, malformed storage");
 } finally { await browser.close(); }
})().catch(error=>{console.error(error);process.exitCode=1;});
