
const assert=require('node:assert/strict');
module.exports=async function verifySync(admin,viewer,baseURL){
 const read=async page=>(await (await page.request.get(baseURL+'/resources/workbench/state')).json());
 const state=await read(admin),id='sync-'+Date.now();state.boards.push({id,name:id,items:[{id:id+'-a',type:'note',title:'A',text:'Initial A'},{id:id+'-b',type:'note',title:'B',text:'Initial B'},{id:id+'-timer',type:'timer',title:'Timer',mode:'5',remaining:300,running:false}]});
 let response=await admin.request.post(baseURL+'/resources/workbench/state',{data:state,headers:{'X-CSRF-Token':await admin.locator('[data-workbench]').getAttribute('data-csrf')}});assert.equal(response.status(),200);
 for(const p of [admin,viewer]){await p.reload();await p.locator('.wb-enhanced').waitFor();await p.locator('.wb-tabs').getByRole('button',{name:id,exact:true}).click();await p.getByRole('button',{name:'编辑',exact:true}).click();}
 const field=(p,suffix)=>p.locator('[data-id="'+id+'-'+suffix+'"] textarea');
 await field(admin,'a').fill('Alice A');await field(viewer,'b').fill('Bob B');
 for(const p of [admin,viewer])await p.locator('[data-workbench][data-save-state=saved]').waitFor();
 const saved=(await read(admin)).boards.find(b=>b.id===id);assert.equal(saved.items[0].text,'Alice A');assert.equal(saved.items[1].text,'Bob B');
 await field(admin,'a').focus();await field(admin,'a').evaluate(e=>{window.focusedNote=e;e.setSelectionRange(3,3)});
 await field(viewer,'b').fill('Remote B while Alice types');await viewer.locator('[data-workbench][data-save-state=saved]').waitFor();
 await admin.waitForFunction(({id})=>document.querySelector('[data-id="'+id+'-b"] textarea').value==='Remote B while Alice types',{id});
 assert(await admin.evaluate(()=>document.activeElement===window.focusedNote&&window.focusedNote.selectionStart===3));
 // Reordering and timer completion must not destroy a member's active input.
 const data=await read(viewer),board=data.boards.find(b=>b.id===id);board.items=[board.items[1],board.items[0],board.items[2]];board.items[2].remaining=1;board.items[2].running=true;board.items[2].started=Date.now();
 response=await viewer.request.post(baseURL+'/resources/workbench/state',{data,headers:{'X-CSRF-Token':await viewer.locator('[data-workbench]').getAttribute('data-csrf')}});assert.equal(response.status(),200);
 await admin.waitForTimeout(1700);assert(await admin.evaluate(()=>document.activeElement===window.focusedNote));
 await field(viewer,'a').focus();await field(viewer,'a').fill('Remote A conflict');await viewer.locator('[data-workbench][data-save-state=saved]').waitFor();
 await field(admin,'a').fill('Local A preserved');await admin.locator('.wb-conflict').waitFor();assert.equal(await field(admin,'a').inputValue(),'Local A preserved');
 await field(admin,'b').fill('Other module still saves');await admin.waitForTimeout(700);assert.equal((await read(admin)).boards.find(b=>b.id===id).items.find(w=>w.id===id+'-b').text,'Other module still saves');
 await admin.locator('[data-id="'+id+'-a"]').getByRole('button',{name:'保存本地版本',exact:true}).click();await admin.locator('[data-workbench][data-save-state=saved]').waitFor();assert.equal((await read(admin)).boards.find(b=>b.id===id).items.find(w=>w.id===id+'-a').text,'Local A preserved');
 // EventSource reconnect refreshes data saved while this client was offline.
 await admin.getByRole('button',{name:'查看',exact:true}).click();await admin.context().setOffline(true);
 await field(viewer,'b').fill('Saved during disconnect');await viewer.locator('[data-workbench][data-save-state=saved]').waitFor();await admin.context().setOffline(false);
 await admin.waitForFunction(({id})=>document.querySelector('[data-id="'+id+'-b"] textarea').value==='Saved during disconnect',{id},{timeout:15000});
};
