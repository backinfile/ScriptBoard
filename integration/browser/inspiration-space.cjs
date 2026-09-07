
const assert=require('node:assert/strict');
module.exports=async function verifySharedSpace(page,baseURL,screenshot){
 await page.context().addCookies([{name:'scriptboard_locale',value:'zh-CN',url:baseURL}]);
 const style=()=>{const n=document.querySelector('main h1'),s=getComputedStyle(n),h=n.closest('header');return {fontSize:s.fontSize,fontWeight:s.fontWeight,lineHeight:s.lineHeight,padding:getComputedStyle(h).padding,background:getComputedStyle(h).backgroundColor,borderBottom:getComputedStyle(h).borderBottom};};
 await page.goto(baseURL+'/resources/databases');const databaseStyle=await page.evaluate(style);
 await page.goto(baseURL+'/settings/users');const token=await page.locator('input[name=csrf_token]').first().inputValue();
 const username='inspiration-viewer-'+Date.now();const created=await page.request.post(baseURL+'/settings/users',{form:{csrf_token:token,username,role:'viewer'}});assert.equal(created.status(),201);const match=(await created.text()).match(/data-generated-password>([^<]+)</);assert(match);
 const context=await page.context().browser().newContext({locale:'zh-CN'});const viewer=await context.newPage();await viewer.goto(baseURL+'/login');await viewer.locator('[name=username]').fill(username);await viewer.locator('[name=password]').fill(match[1]);await viewer.locator('[data-login-form] button[type=submit]').click();await viewer.waitForURL('**/monitor');await viewer.goto(baseURL+'/resources/workbench');await viewer.locator('.wb-enhanced').waitFor();
 await page.goto(baseURL+'/resources/workbench');await page.locator('.wb-enhanced').waitFor();assert.equal(await page.locator('main h1').first().textContent(),'灵感空间');assert.deepEqual(await page.evaluate(style),databaseStyle);
 const read=async p=>(await (await p.request.get(baseURL+'/resources/workbench/state')).json());assert.deepEqual((await read(page)).boards,(await read(viewer)).boards);
 await viewer.getByRole('button',{name:'编辑',exact:true}).click();await viewer.getByRole('button',{name:'新建空间',exact:true}).click();await viewer.getByRole('dialog').getByRole('textbox',{name:'空间名称',exact:true}).fill('全员共享验收 '+username);await viewer.getByRole('dialog').getByRole('button',{name:'创建空间',exact:true}).click();await viewer.locator('[data-add=note]').click();await viewer.locator('.wb-note textarea').fill('来自另一位登录成员的记录');await viewer.locator('[data-workbench][data-save-state=saved]').waitFor();
 await page.locator('.wb-tabs').getByRole('button',{name:'全员共享验收 '+username,exact:true}).waitFor({timeout:12000});await page.getByRole('button',{name:'全员共享验收 '+username,exact:true}).click();assert.equal(await page.locator('.wb-note textarea').inputValue(),'来自另一位登录成员的记录');
 await require('./workbench-tab-switch.cjs')(page);
 if(screenshot)await page.screenshot({path:screenshot});
 const anonymous=await page.context().browser().newContext();const response=await anonymous.request.get(baseURL+'/resources/workbench/state',{maxRedirects:0});assert.notEqual(response.status(),200);await anonymous.close();await require('./inspiration-sync.cjs')(page,viewer,baseURL);await context.close();
};
