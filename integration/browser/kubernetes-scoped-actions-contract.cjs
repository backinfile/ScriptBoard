const assert=require("node:assert/strict");
const fs=require("node:fs");
const path=require("node:path");
const {chromium}=require("playwright");
const snapshot=(url,revision)=>{
 const q=new URL(url).searchParams;
 const link=(key,value)=>"/monitor/kubernetes?cluster="+(q.get("cluster")||"one")+"&"+key+"="+value;
 return '<!doctype html><html><body><main data-kubernetes-page data-kubernetes-refreshed="Updated" data-kubernetes-refresh-failed="Refresh failed"><div class="kubernetes-refresh-toolbar"><button data-kubernetes-refresh>Refresh</button><span data-kubernetes-refresh-status></span></div><nav class="kubernetes-page-tabs"><a aria-current="page" href="'+link("status","all")+'">Monitor</a></nav><div class="kubernetes-monitor-summary"><section class="kubernetes-cluster-switch"><form method="get" action="/monitor/kubernetes"><select name="cluster" data-kubernetes-cluster-select><option value="one">one</option><option value="two" '+(q.get("cluster")==="two"?"selected":"")+'>two</option></select><button>Switch</button></form></section></div><div data-kubernetes-alert-slot>Alert</div><details data-kubernetes-section="external"><summary>External<span data-kubernetes-section-facts="external">2</span></summary><div data-kubernetes-external-body>Services '+revision+'</div></details><details data-kubernetes-section="workloads"><summary>Workloads<span data-kubernetes-section-facts="workloads">'+revision+'</span></summary><div class="kubernetes-workload-controls"><nav class="kubernetes-status-tabs">'+["all","ready","progressing","degraded"].map(status=>'<a href="'+link("status",status)+'">'+status+'</a>').join("")+'</nav><form class="kubernetes-toolbar" method="get" action="/monitor/kubernetes"><input name="query" value="'+(q.get("query")||"")+'"><select name="namespace"><option>all</option><option>production</option></select><select name="kind"><option>all</option><option>Deployment</option></select><input type="hidden" name="cluster" value="'+(q.get("cluster")||"one")+'"><button>Search</button></form></div><div class="kubernetes-table-shell" data-revision="'+revision+'">'+["name","namespace","status","ready","node","cpu","memory","restarts"].map(sort=>'<a class="kubernetes-sort-link" href="'+link("sort",sort)+'">'+sort+'</a>').join("")+'<form method="post" action="/monitor/kubernetes/clusters/one/workloads/production/Deployment/api/operate" data-kubernetes-confirm="Confirm redeploy"><input name="csrf_token" type="hidden" value="test"><input name="operation" type="hidden" value="redeploy"><button>Redeploy</button></form></div></details><details data-kubernetes-section="nodes"><summary>Nodes<span data-kubernetes-section-facts="nodes">1</span></summary><div data-kubernetes-node-list>Nodes '+revision+'</div></details></main></body></html>';
};
(async()=>{
const browser=await chromium.launch({headless:true});
try{
const page=await browser.newPage();let revision=0;let posts=0;let fail=false;
await page.route("http://kubernetes.test/**",async route=>{
 const request=route.request();
 if(request.method()==="POST"){posts++;assert(request.postData().includes('return_to'));assert(request.postData().includes('csrf_token'));}
 await route.fulfill(fail?{status:503,body:"Unavailable"}:{contentType:"text/html",body:snapshot(request.url(),++revision)});
});
await page.goto("http://kubernetes.test/monitor/kubernetes?cluster=one");
await page.addScriptTag({path:path.resolve(__dirname,"../../internal/web/ui/assets/app.js")});
await page.locator('[data-kubernetes-section="workloads"] > summary').click();
const mark=()=>page.evaluate(()=>{window.preserved=[document.querySelector("main"),document.querySelector(".kubernetes-monitor-summary"),document.querySelector("[data-kubernetes-external-body]"),document.querySelector("[data-kubernetes-node-list]")];});
const stable=async()=>{
 await page.waitForFunction(()=>!document.querySelector("main").hasAttribute("aria-busy"));
 assert(await page.evaluate(()=>window.preserved.every(e=>e.isConnected)),"unrelated regions must remain mounted");
 assert.equal(await page.locator('[data-kubernetes-section="workloads"]').getAttribute("open"),"");
};
await mark();
for(const status of ["ready","progressing","degraded","all"]){await page.locator('.kubernetes-status-tabs a').filter({hasText:new RegExp("^"+status+"$")}).click();await stable();}
for(const sort of ["name","namespace","status","ready","node","cpu","memory","restarts"]){await page.locator(".kubernetes-sort-link").filter({hasText:new RegExp("^"+sort+"$")}).click();await stable();}
await page.locator('[name=query]').fill("api");
await page.locator('[name=namespace]').selectOption("production");
await page.locator('[name=kind]').selectOption("Deployment");
await page.locator(".kubernetes-toolbar button").click();await stable();
assert.equal(new URL(page.url()).searchParams.get("query"),"api");
assert.equal(new URL(page.url()).searchParams.get("namespace"),"production");
const before=page.url();
await page.evaluate(()=>history.back());await page.waitForURL(url=>url.href!==before);await stable();
await page.evaluate(()=>history.forward());await page.waitForURL(before);await stable();
await page.locator('[data-kubernetes-confirm] button').click();
assert.equal(posts,0);
await page.locator('dialog[open] button').filter({hasText:"Redeploy"}).click();await stable();
assert.equal(posts,1);assert.equal(page.url(),before);
fail=true;const old=await page.locator(".kubernetes-table-shell").getAttribute("data-revision");
await page.locator('.kubernetes-status-tabs a').first().click();await stable();
assert.equal(await page.locator(".kubernetes-table-shell").getAttribute("data-revision"),old);
fail=false;
await page.locator('[name=cluster]').first().selectOption("two");
await page.waitForURL(url=>url.searchParams.get("cluster")==="two");
assert(await page.evaluate(()=>window.preserved[0].isConnected),"cluster switching preserves the page");
assert.equal(await page.locator('[data-kubernetes-section="workloads"]').getAttribute("open"),"");
console.log("PASS: status filters, eight sorts, search/namespace/kind, back/forward, confirmed operation, failure preservation, cluster switch.");
}finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
