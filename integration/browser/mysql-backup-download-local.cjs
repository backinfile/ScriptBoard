const {chromium}=require("playwright");
const assert=require("node:assert/strict"),fs=require("node:fs"),path=require("node:path"),crypto=require("node:crypto");
(async()=>{const browser=await chromium.launch({executablePath:process.env.BROWSER_EXECUTABLE||"C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",headless:true});try{
const context=await browser.newContext({acceptDownloads:true,viewport:{width:1500,height:1000}});const page=await context.newPage();const base=process.env.DATABASE_APP_URL||"http://127.0.0.1:19756";
await page.goto(base+"/login");await page.locator("[name=username]").fill("admin");await page.locator("[name=password]").fill("calibration-ledger-2026");await page.locator('form[action="/login"] button[type=submit]').click();await page.waitForURL("**/monitor");
await page.goto(base+"/resources/databases?instance=qa-mysql&tab=backups");
const requests=[];page.on("request",r=>{if(r.url().includes("/qa-large-download/download"))requests.push({type:r.resourceType(),url:r.url()})});
let download=null;page.on("download",d=>download=d);const initial=page.url();const started=Date.now();await page.locator('a[href$="/qa-large-download/download"]').click();
await page.waitForTimeout(2500);console.log(JSON.stringify({requests,download:!!download,urlUnchanged:page.url()===initial,elapsed:Date.now()-started}));
assert.ok(download,"Expected native browser download; page navigation intercepted the file request");assert.equal(requests.some(r=>r.type==="fetch"),false,"Download must not be fetched as an HTML page");
await download.saveAs(path.resolve(".scratch/database-enhancements/downloaded.sql.gz"));assert.equal(fs.statSync(".scratch/database-enhancements/downloaded.sql.gz").size,44*1024*1024);assert.equal(crypto.createHash("sha256").update(fs.readFileSync(".scratch/database-enhancements/downloaded.sql.gz")).digest("hex"),crypto.createHash("sha256").update(fs.readFileSync(".scratch/database-enhancements/deployment/state/database-backups/mysql/large-download.sql.gz")).digest("hex"));
await page.locator(".mysql-tabs a").filter({hasText:/备份计划|Backup plans/}).click();await page.locator("[data-mysql-plan-details-link]").first().waitFor();console.log("PASS: native 44 MiB download and page remains usable");
}finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1});
