/* Inspiration space: DOM rendering owns content; canvas coordinates stay in CSS pixels. */
window.ScriptBoardWorkbench = function (root, renderIcons) {
  const en = root.dataset.locale === 'en-US';
  const t = (zh, english) => en ? english : zh;
  const labels = {note:t('笔记','Note'),links:t('链接','Links'),todo:t('待办','Tasks'),timer:t('计时','Timer'),draw:t('画板','Canvas')};
  const icons = {note:'square-pen',links:'link',todo:'list-checks',timer:'timer',draw:'pencil'};
  const el = (tag, cls, text) => { const n=document.createElement(tag); if(cls)n.className=cls; if(text!==undefined)n.textContent=text; return n; };
  const icon = name => {const n=el('span');n.dataset.lucide=name;n.setAttribute('aria-hidden','true');return n;};
  const button = (text, action, name, cls='') => {const n=el('button',cls);n.type='button';if(name)n.append(icon(name));n.append(document.createTextNode(text));n.onclick=action;return n;};
  const smallButton=(label,action,name)=>{const n=button('',action,name,'wb-icon');n.setAttribute('aria-label',label);n.title=label;return n;};
  const input=(label,value,action)=>{const n=el('input');n.value=value||'';n.setAttribute('aria-label',label);n.oninput=()=>action(n.value);return n;};
  // Plain HTTP still supports random bytes, even when randomUUID requires a secure context.
  const uid=()=>crypto.randomUUID?.()||Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('');
  const copy=value=>JSON.parse(JSON.stringify(value));
  const canonical=v=>{if(Array.isArray(v))return v.map(canonical);if(v&&typeof v==='object'){const out={};Object.keys(v).sort().forEach(k=>{if(k==='revision'||k==='capacity')return;const value=canonical(v[k]);if(value!==undefined&&value!==null&&value!==''&&value!==false&&value!==0&&!(typeof value==='object'&&Object.keys(value).length===0))out[k]=value;});return out;}return v;};
  const equal=(a,b)=>JSON.stringify(canonical(a))===JSON.stringify(canonical(b));
  const state=JSON.parse(root.querySelector('[data-wb-state]').value);
  const capacity=state.capacity||{boards:32,items:300,points:60000};
  let boards=state.boards||[], revision=state.revision, current=boards[0]?.id, edit=false, version=0, saved=0, saving=false, failure='', conflict=false, alive=true, pending=null, deleted=null;
  let widgetsCleanup=[], strokesTotal=0, saveWaiters=[];
  let baseline=copy(state), latest=copy(state);
  const conflicts=new Set(), activeModules=new Set(), cardCleanup=new Map();
  const dirty=()=>!equal(boards,baseline.boards);
  function cleanupCards(){cardCleanup.forEach(list=>list.forEach(f=>f()));cardCleanup.clear();widgetsCleanup.splice(0).forEach(f=>f());}
  const app=root.querySelector('[data-wb-app]');app.hidden=false;root.querySelector('[data-wb-fallback]').hidden=true;root.classList.add('wb-enhanced');
  const top=el('header','page-heading mysql-page-heading wb-top'), heading=el('div');heading.append(el('p','page-eyebrow',t('资源 / 灵感空间','Resources / Inspiration space')),el('h1','',t('灵感空间','Inspiration space')),el('p','',t('一起记录想法、整理资料，让灵感成为下一步行动。','Capture ideas, collect references, and turn inspiration into action together.')));top.append(heading);
  const boardbar=el('div','wb-boardbar'),tabs=el('div','wb-tabs');
  const scroll=el('div','wb-scroll');scroll.tabIndex=0;scroll.setAttribute('aria-label',t('面板内容','Board contents'));
  const tools=el('div','wb-tools'),name=input(t('面板名称','Board name'),'',value=>{const b=board();if(b){b.name=value;changed();updateTabs();}});name.maxLength=80;
  name.onblur=()=>{if(board()&&!board().name.trim()){board().name=t('未命名面板','Untitled board');name.value=board().name;changed();updateTabs();}};
  const search=input(t('查找面板内容','Search board'),'',()=>render());search.placeholder=t('查找面板内容','Search board');search.type='search';
  const status=el('span','wb-status'), retry=button(t('重试保存','Retry save'),()=>{failure='';flush();},null,'wb-quiet'), backup=button(t('下载未保存内容','Download unsaved data'),()=>{const u=URL.createObjectURL(new Blob([JSON.stringify({revision,boards},null,2)],{type:'application/json'}));const a=el('a');a.href=u;a.download='workbench-unsaved.json';a.click();setTimeout(()=>URL.revokeObjectURL(u),1000);},null,'wb-quiet');
  const notice=el('div','wb-notice');notice.setAttribute('role','status');notice.append(status,retry,backup);
  const grid=el('div','wb-grid');
  const dock=el('div','wb-dock');dock.setAttribute('aria-label',t('工作台工具栏','Workspace toolbar'));
  const modes=el('div','wb-modes');modes.setAttribute('role','group');modes.setAttribute('aria-label',t('工作台模式','Workspace mode'));
  const viewButton=button(t('查看','View'),()=>setMode(false),'eye'),editButton=button(t('编辑','Edit'),()=>setMode(true),'square-pen');modes.append(viewButton,editButton);dock.append(modes);
  Object.keys(labels).forEach(type=>{const b=button(labels[type],()=>{if(!board())newBoard();if(board().items.length>=100||boards.reduce((n,b)=>n+b.items.length,0)>=capacity.items)return message(t('模块数量已达上限','Block limit reached'));const w={id:uid(),type,title:labels[type]};if(type==='note')w.text='';if(type==='todo')w.tasks=[];if(type==='links')w.links=[];if(type==='timer')Object.assign(w,{mode:'25',remaining:1500,running:false,started:0});if(type==='draw')Object.assign(w,{size:'medium',pan:{x:0,y:0},strokes:[]});board().items.push(w);search.value='';changed();render();grid.querySelector('[data-id="'+w.id+'"]').scrollIntoView({block:'nearest'});},icons[type],'wb-edit-only');b.dataset.add=type;dock.append(b);});
  const restore=button(t('撤销删除','Undo delete'),()=>{if(deleted){deleted.restore();deleted=null;changed();render();}},'undo-2','wb-edit-only');dock.append(restore);
  const newBoardButton=button(t('新建面板','New board'),()=>{newBoard();render();name.focus();name.select();},'plus','wb-edit-only');
  boardbar.append(tabs,newBoardButton);tools.append(name,search);scroll.append(tools,notice,grid);app.append(top,boardbar,scroll,dock);
  function board(){return boards.find(b=>b.id===current);}
  function newBoard(){if(boards.length>=capacity.boards){message(t('面板数量已达上限','Board limit reached'));return;}const b={id:uid(),name:t('未命名面板','Untitled board'),items:[]};boards.push(b);current=b.id;changed();}
  function message(s){status.textContent=s;notice.hidden=false;}
  function saveStatus(){notice.hidden=false;conflict=conflicts.size>0;retry.hidden=!failure;backup.hidden=!failure&&!conflict;status.textContent=failure||(conflict?t('部分模块或面板存在冲突，本地内容已保留；其他模块可继续保存。','Some blocks or boards conflict. Local content is preserved; other blocks can still save.'):null)||(saving||dirty()?t('正在保存…','Saving…'):t('已保存 · 全员共享','Saved · Shared with everyone'));}
  function changed(){version++;clearTimeout(pending);pending=setTimeout(flush,350);saveStatus();}
  async function flush(){
    clearTimeout(pending);
    if(saving){await new Promise(resolve=>saveWaiters.push(resolve));return flush();}
    if(!dirty())return;
    const sending=version, sent={revision,boards:copy(boards)}, before=copy(baseline);
    saving=true;failure='';saveStatus();
    try{
      const response=await fetch('/resources/workbench/state',{method:'PATCH',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':root.dataset.csrf},body:JSON.stringify({base:before,next:sent}),signal:AbortSignal.timeout(15000)});
      if(!response.ok)throw Error(t('保存失败。内容仍在此页面，请重试或下载备份。','Save failed. Your edits remain here. Retry or download a backup.'));
      const result=await response.json();
      if(!Number.isSafeInteger(result.state?.revision))throw Error(t('保存响应无效，请重试。','Invalid save response. Please retry.'));
      acknowledge(before,sent,result);
      conflicts.clear();(result.conflicts||[]).forEach(key=>conflicts.add(key));
      syncState(result.state);
      saved=sending;
    }catch(e){failure=e.message;}
    finally{saving=false;saveWaiters.splice(0).forEach(resolve=>resolve());saveStatus();if(!failure&&version!==sending&&alive)flush();if(refreshPending)refreshShared();}
  }

  const findBoard=(state,id)=>(state.boards||[]).find(b=>b.id===id);
  const ids=items=>(items||[]).map(x=>x.id);
  const reorder=(items,order)=>{const byID=new Map(items.map(x=>[x.id,x]));return [...order.filter(id=>byID.has(id)).map(id=>byID.get(id)),...items.filter(x=>!order.includes(x.id))];};
  const replace=(items,id,value)=>{const i=items.findIndex(x=>x.id===id);if(value){if(i<0)items.push(copy(value));else items[i]=copy(value);}else if(i>=0)items.splice(i,1);};
  function protectedItem(id){const card=grid.querySelector('[data-id="'+id+'"]');return activeModules.has(id)||!!card?.querySelector('.wb-link-editor')||!!(card?.contains(document.activeElement)&&!document.activeElement.readOnly&&!document.activeElement.disabled&&document.activeElement.matches('input,textarea,select'));}
  function acknowledge(before,sent,result){
    const blocked=new Set(result.conflicts||[]),remote=result.state;
    for(const old of before.boards){if(!findBoard(sent,old.id)&&!blocked.has('board:'+old.id))replace(baseline.boards,old.id,null);}
    for(const b of sent.boards){const old=findBoard(before,b.id),server=findBoard(remote,b.id);if(!server||blocked.has('board:'+b.id))continue;
      if(!old){replace(baseline.boards,b.id,server);continue;}
      const base=findBoard(baseline,b.id);if(!base)continue;
      if(b.name!==old.name&&!blocked.has('name:'+b.id)){base.name=server.name;base.revision=server.revision;}
      const all=new Set([...ids(old.items),...ids(b.items)]);
      for(const id of all){const a=(old.items||[]).find(w=>w.id===id),n=(b.items||[]).find(w=>w.id===id);if(!equal(a,n)&&!blocked.has('item:'+id))replace(base.items,id,(server.items||[]).find(w=>w.id===id));}
      if(!equal(ids(old.items),ids(b.items))&&!blocked.has('order:'+b.id))base.items=reorder(base.items,ids(b.items));
    }
    if(!equal(ids(before.boards),ids(sent.boards))&&!blocked.has('boards'))baseline.boards=reorder(baseline.boards,ids(sent.boards));
  }
  function syncState(next){
    if(!alive||!next||next.revision<revision)return;
    latest=copy(next);const changedCards=new Set(),previous=current,rootOrderClean=equal(ids(boards),ids(baseline.boards));
    for(const remote of next.boards){if(!findBoard(baseline,remote.id)&&!boards.some(b=>b.id===remote.id)){boards.push(copy(remote));baseline.boards.push(copy(remote));ids(remote.items).forEach(id=>changedCards.add(id));}}
    for(const b of [...boards]){
      const old=findBoard(baseline,b.id),remote=findBoard(next,b.id);if(!old)continue;
      if(!remote){if(equal(b,old)&&!b.items.some(w=>protectedItem(w.id))&&!(b.id===current&&edit&&document.activeElement===name)){boards.splice(boards.indexOf(b),1);replace(baseline.boards,b.id,null);}else if(!equal(b,old))conflicts.add('board:'+b.id);continue;}
      if(b.name===old.name&&(!edit||document.activeElement!==name)){b.name=remote.name;old.name=remote.name;old.revision=remote.revision;}else if(b.name!==old.name&&remote.revision!==old.revision&&b.name!==remote.name)conflicts.add('name:'+b.id);
      const orderClean=equal(ids(b.items),ids(old.items));
      for(const w of [...b.items]){
        const before=(old.items||[]).find(x=>x.id===w.id),server=(remote.items||[]).find(x=>x.id===w.id);if(!before)continue;
        if(equal(w,server)){w.revision=server?.revision;replace(old.items,w.id,server);conflicts.delete('item:'+w.id);continue;}
        if(equal(w,before)&&!protectedItem(w.id)){replace(b.items,w.id,server);replace(old.items,w.id,server);changedCards.add(w.id);conflicts.delete('item:'+w.id);}
        else if(!equal(w,before)&&(!server||server.revision!==before.revision))conflicts.add('item:'+w.id);
      }
      for(const w of remote.items||[]){if(!(old.items||[]).some(x=>x.id===w.id)&&!b.items.some(x=>x.id===w.id)){b.items.push(copy(w));old.items.push(copy(w));changedCards.add(w.id);}}
      if(orderClean&&!b.items.some(w=>protectedItem(w.id))){b.items=reorder(b.items,ids(remote.items));old.items=reorder(old.items,ids(remote.items));}
    }
    if(rootOrderClean){boards=reorder(boards,ids(next.boards));baseline.boards=reorder(baseline.boards,ids(next.boards));}
    revision=next.revision;baseline.revision=revision;
    if(!board())current=boards[0]?.id;
    if(previous!==current){render();}else{updateTabs();if(document.activeElement!==name)name.value=board()?.name||'';refreshCards(changedCards);}
    showConflicts();saveStatus();
  }
  function refreshCards(changedCards){
    const query=search.value.trim().toLowerCase(),items=(board()?.items||[]).filter(w=>(w.title+' '+(w.text||'')+' '+JSON.stringify(w.tasks||[])+' '+JSON.stringify(w.links||[])).toLowerCase().includes(query));
    const wanted=new Set(ids(items));
    for(const card of [...grid.children]){if(!card.dataset.id||!wanted.has(card.dataset.id)){(cardCleanup.get(card.dataset.id)||[]).forEach(f=>f());cardCleanup.delete(card.dataset.id);card.remove();}}
    let position=grid.firstElementChild;
    for(const w of items){let card=grid.querySelector('[data-id="'+w.id+'"]');if(card&&changedCards.has(w.id)){(cardCleanup.get(w.id)||[]).forEach(f=>f());cardCleanup.delete(w.id);const fresh=buildCard(w);card.replaceWith(fresh);if(position===card)position=fresh;card=fresh;}if(!card)card=buildCard(w);if(card!==position)grid.insertBefore(card,position);position=card.nextElementSibling;}
    if(!items.length)grid.append(el('div','wb-empty',t('切换到编辑模式，从下方工具栏添加内容。','Switch to Edit and add content from the toolbar.')));
    renderIcons(root);
  }
  function showConflicts(){
    conflict=conflicts.size>0;
    for(const card of grid.querySelectorAll('[data-id]')){
      const key='item:'+card.dataset.id;let banner=card.querySelector('.wb-conflict');if(!conflicts.has(key)){banner?.remove();continue;}if(banner)continue;
      banner=el('div','wb-conflict');banner.setAttribute('role','alert');banner.append(el('span','',t('此模块有其他成员的修改，本地内容已保留。','Another member changed this block. Your local content is preserved.')));
      const resolve=keep=>{const b=board(),remote=findBoard(latest,b.id),server=remote?.items.find(w=>w.id===card.dataset.id);if(!remote){message(t('面板已被删除，请先下载本地备份。','The board was deleted. Download your local backup first.'));return;}const old=findBoard(baseline,b.id);replace(old.items,card.dataset.id,server);if(!keep)replace(b.items,card.dataset.id,server);conflicts.delete(key);changed();refreshCards(new Set([card.dataset.id]));showConflicts();};
      banner.append(button(t('采用共享版本','Use shared version'),()=>resolve(false),null,'wb-quiet'),button(t('保存本地版本','Save local version'),()=>resolve(true),null,'wb-quiet'));card.append(banner);
    }
  }

  function updateTabs(){tabs.replaceChildren();boards.forEach(b=>{const n=button(b.name||t('未命名面板','Untitled board'),()=>{current=b.id;search.value='';render();});n.setAttribute('aria-pressed',String(b.id===current));n.classList.toggle('wb-selected',b.id===current);tabs.append(n);});}
  function setMode(value){edit=value;root.dataset.mode=edit?'edit':'view';name.readOnly=!edit;viewButton.setAttribute('aria-pressed',String(!edit));editButton.setAttribute('aria-pressed',String(edit));render();}
  function render(){cleanupCards();grid.replaceChildren();updateTabs();name.value=board()?.name||'';name.disabled=!board();name.readOnly=!edit;restore.hidden=!deleted;const q=search.value.trim().toLowerCase();const items=(board()?.items||[]).filter(w=>(w.title+' '+(w.text||'')+' '+JSON.stringify(w.tasks||[])+' '+JSON.stringify(w.links||[])).toLowerCase().includes(q));if(!items.length)grid.append(el('div','wb-empty',q?t('没有找到匹配内容','No matching content'):t('切换到编辑模式，从下方工具栏添加内容。','Switch to Edit and add content from the toolbar.')));
    if(board()){const del=button(t('删除面板','Delete board'),()=>{const old=board(),index=boards.indexOf(old);if(old.items.length&&!confirm(t('删除此面板及其中内容？可通过撤销删除恢复。','Delete this board and its contents? You can undo this action.')))return;boards.splice(index,1);current=boards[0]?.id;deleted={restore:()=>{boards.splice(index,0,old);current=old.id;}};changed();render();},'trash-2','wb-edit-only wb-quiet');tools.querySelector('button')?.remove();tools.insertBefore(del,search);}else tools.querySelector('button')?.remove();
    for(const w of items)grid.append(buildCard(w));
    renderIcons(root);showConflicts();saveStatus();
  }
  function buildCard(w){const cleanupStart=widgetsCleanup.length;const card=el('section','wb-widget wb-'+w.type);card.dataset.id=w.id;const head=el('header','wb-widget-head');const heading=input(t('模块标题','Block title'),w.title,v=>{w.title=v;changed();});heading.maxLength=80;heading.readOnly=!edit;head.append(icon(icons[w.type]),heading);const controls=el('div','wb-reorder wb-edit-only');[-1,1].forEach(d=>controls.append(smallButton(d<0?t('向前移动','Move earlier'):t('向后移动','Move later'),()=>{const a=board().items,i=a.indexOf(w),j=i+d;if(j>=0&&j<a.length){[a[i],a[j]]=[a[j],a[i]];changed();render();}},d<0?'chevron-up':'chevron-down')));controls.append(smallButton(t('删除模块','Delete block'),()=>{const b=board(),index=b.items.indexOf(w);b.items.splice(index,1);deleted={restore:()=>b.items.splice(index,0,w)};changed();render();},'x'));head.append(controls);head.draggable=edit;head.ondragstart=e=>{if(e.target.closest('input,button'))return e.preventDefault();e.dataTransfer.setData('text/plain',w.id);};card.ondragover=e=>{if(edit)e.preventDefault();};card.ondrop=e=>{if(!edit)return;e.preventDefault();const a=board().items,i=a.findIndex(x=>x.id===e.dataTransfer.getData('text/plain'));if(i<0)return;const j=a.indexOf(w);a.splice(j,0,a.splice(i,1)[0]);changed();render();};const body=el('div','wb-widget-body');card.append(head,body);if(w.type==='note'){const text=el('textarea');text.value=w.text||'';text.readOnly=!edit;text.maxLength=30000;text.placeholder=t('写下你的想法…','Write your thoughts…');text.setAttribute('aria-label',t('笔记内容','Note content'));text.oninput=()=>{w.text=text.value;changed();};body.append(text);}if(w.type==='todo')tasks(body,w);if(w.type==='links')links(body,w);if(w.type==='timer')timer(body,w);if(w.type==='draw')drawing(body,w,card);cardCleanup.set(w.id,widgetsCleanup.splice(cleanupStart));return card;}
  function tasks(body,w){w.tasks=w.tasks||[];for(const task of w.tasks){const row=el('div','wb-task');const check=el('input');check.type='checkbox';check.checked=task.done;check.setAttribute('aria-label',t('完成待办','Complete task'));check.onchange=()=>{task.done=check.checked;row.classList.toggle('wb-done',task.done);changed();};row.classList.toggle('wb-done',task.done);row.append(check);if(edit){const text=input(t('待办内容','Task text'),task.text,v=>{task.text=v;changed();});text.maxLength=1000;row.append(text,smallButton(t('删除此待办','Delete task'),()=>{const i=w.tasks.indexOf(task);w.tasks.splice(i,1);deleted={restore:()=>w.tasks.splice(i,0,task)};changed();render();},'x'));}else row.append(el('span','',task.text));body.append(row);}if(edit){const add=el('form','wb-add-row');add.dataset.native='';const text=input(t('添加待办','Add task'),'',()=>{});text.placeholder=t('添加待办，按 Enter 确认','Add a task and press Enter');text.maxLength=1000;add.append(text);add.onsubmit=e=>{e.preventDefault();if(text.value.trim()){if(w.tasks.length>=200)return message(t('最多 200 条待办','Maximum 200 tasks'));w.tasks.push({text:text.value.trim(),done:false});changed();render();}};body.append(add);}}
  function links(body,w){w.links=w.links||[];const valid=v=>{const u=new URL(v);if(!['http:','https:'].includes(u.protocol)||u.username||u.password)throw Error();return u;};for(const link of w.links){const row=el('div','wb-link-row'),a=el('a');a.href=link.url;a.target='_blank';a.rel='noopener noreferrer';a.dataset.native='';const text=el('span');text.append(el('b','',link.title),el('small','',link.url));a.append(icon('link'),text,icon('external-link'));row.append(a);if(edit){row.append(smallButton(t('编辑此链接','Edit link'),()=>{if(row.querySelector('form'))return;const form=el('form','wb-link-editor');form.dataset.native='';const title=input(t('链接标题','Link title'),link.title,()=>{}),url=input(t('链接地址','Link URL'),link.url,()=>{});title.maxLength=160;url.maxLength=8192;title.required=true;url.required=true;form.append(title,url);const save=button(t('保存链接','Save link'),()=>{},null);save.type='submit';const error=el('span','wb-error');error.setAttribute('role','alert');form.append(error,save,button(t('取消','Cancel'),()=>form.remove(),null,'wb-quiet'));form.onsubmit=e=>{e.preventDefault();try{link.url=valid(url.value.trim()).href;link.title=title.value.trim()||new URL(link.url).hostname;changed();render();}catch{error.textContent=t('请输入有效的 HTTP 或 HTTPS 地址','Enter a valid HTTP or HTTPS URL');}};row.append(form);title.focus();},'square-pen'),smallButton(t('删除此链接','Delete link'),()=>{const i=w.links.indexOf(link);w.links.splice(i,1);deleted={restore:()=>w.links.splice(i,0,link)};changed();render();},'x'));}body.append(row);}if(edit){const form=el('form','wb-add-row');form.dataset.native='';const url=input(t('收藏链接','Save URL'),'',()=>{});url.placeholder=t('粘贴链接，回车收藏','Paste a URL and press Enter');url.maxLength=8192;const add=button(t('添加','Add'),()=>{},'plus');add.type='submit';form.append(url,add);form.onsubmit=e=>{e.preventDefault();try{if(w.links.length>=200)return message(t('最多 200 条链接','Maximum 200 links'));const u=valid(url.value.includes('://')?url.value:'https://'+url.value);w.links.push({title:u.hostname,url:u.href});changed();render();}catch{message(t('请输入有效的 HTTP 或 HTTPS 地址','Enter a valid HTTP or HTTPS URL'));}};body.append(form);}}
  function seconds(w){const delta=w.running?Math.max(0,Math.floor((Date.now()-w.started)/1000)):0;return w.mode==='0'?w.remaining+delta:Math.max(0,w.remaining-delta);}
  const format=n=>String(Math.floor(n/60)).padStart(2,'0')+':'+String(n%60).padStart(2,'0');
  function timer(body,w){const select=el('select');select.setAttribute('aria-label',t('计时模式','Timer mode'));[['25',t('专注 · 25 分钟','Focus · 25 minutes')],['5',t('休息 · 5 分钟','Break · 5 minutes')],['0',t('正向计时','Stopwatch')]].forEach(([value,text])=>{const o=el('option','',text);o.value=value;select.append(o);});select.value=w.mode;const digits=el('div','wb-digits');digits.dataset.timer=w.id;const hint=el('small','',t('一次只做一件事。','One thing at a time.'));const toggle=button('',()=>{if(w.running){w.remaining=seconds(w);w.running=false;}else{if(w.mode!=='0'&&w.remaining<=0)w.remaining=Number(w.mode)*60;w.started=Date.now();w.running=true;}changed();update();},'play');toggle.dataset.timerToggle=w.id;const update=()=>{digits.textContent=format(seconds(w));toggle.lastChild.textContent=w.running?t('暂停','Pause'):t('开始','Start');};select.onchange=()=>{w.mode=select.value;w.remaining=Number(w.mode)*60;w.running=false;changed();update();};const actions=el('div','wb-timer-actions');actions.append(toggle,smallButton(t('重置计时','Reset timer'),()=>{w.running=false;w.remaining=Number(w.mode)*60;changed();update();},'rotate-ccw'));body.append(select,digits,hint,actions);update();widgetsCleanup.push(()=>{});}
  function drawing(body,w,card){w.strokes=w.strokes||[];w.pan=w.pan||{x:0,y:0};card.dataset.size=w.size||'medium';body.classList.add('wb-canvas-body');const nav=el('div','wb-canvas-nav'),viewport=el('div','wb-canvas-viewport'),canvas=el('canvas');canvas.setAttribute('aria-label',t('自由绘画区域，移动模式下可用方向键平移','Drawing area. In move mode, use arrow keys to pan.'));canvas.tabIndex=0;viewport.append(canvas);const foot=el('div','wb-canvas-footer wb-edit-only');body.append(nav,viewport,foot);let tool=edit?'draw':'pan',color='#3b5bfd',gesture=null,stroke=null,shapeStart=null,pointer=null,raf=0;
    const draw=button(t('画笔','Pen'),()=>{tool='draw';updateTools();},'pencil','wb-edit-only'),pan=button(t('移动画布','Move canvas'),()=>{tool='pan';updateTools();},'move');nav.append(draw,pan,button(t('回到原点','Reset view'),()=>{w.pan={x:0,y:0};changed();paint();},'locate-fixed','wb-quiet'));
    const shapeTools=[['line',t('直线','Line'),'minus'],['arrow',t('箭头','Arrow'),'arrow-right'],['rectangle',t('矩形','Rectangle'),'square'],['ellipse',t('椭圆','Ellipse'),'circle']].map(([value,label,name])=>{const b=button(label,()=>{tool=value;updateTools();},name,'wb-edit-only');b.dataset.shape=value;nav.insertBefore(b,pan);return b;});
    function updateTools(){shapeTools.forEach(b=>b.setAttribute('aria-pressed',String(tool===b.dataset.shape)));draw.setAttribute('aria-pressed',String(tool==='draw'));pan.setAttribute('aria-pressed',String(tool==='pan'));canvas.dataset.tool=tool;}updateTools();
    const sizes=el('div','wb-sizes');sizes.setAttribute('role','group');sizes.setAttribute('aria-label',t('画板尺寸','Canvas size'));[['small',t('小','S')],['medium',t('中','M')],['large',t('大','L')],['xlarge',t('超大','XL')]].forEach(([size,label])=>{const b=button(label,()=>{w.size=size;card.dataset.size=size;sizes.querySelectorAll('button').forEach(x=>x.setAttribute('aria-pressed',String(x===b)));changed();if(size==='xlarge')requestAnimationFrame(()=>scroll.scrollTo({top:scroll.scrollTop+card.getBoundingClientRect().top-scroll.getBoundingClientRect().top-8}));});b.dataset.size=size;b.setAttribute('aria-pressed',String(size===card.dataset.size));sizes.append(b);});
    ['#3b5bfd','#343b49'].forEach((c,i)=>{const b=smallButton(i?t('石墨色画笔','Graphite pen'):t('蓝色画笔','Blue pen'),()=>{color=c;foot.querySelectorAll('[data-color]').forEach(x=>x.setAttribute('aria-pressed',String(x===b)));},'circle');b.dataset.color=c;b.style.color=c;b.setAttribute('aria-pressed',String(!i));foot.append(b);});foot.append(smallButton(t('撤销一笔','Undo stroke'),()=>{w.strokes.pop();changed();paint();},'undo-2'),sizes,button(t('清空','Clear'),()=>{const previous=w.strokes;w.strokes=[];deleted={restore:()=>{w.strokes=previous;}};restore.hidden=false;changed();paint();},null,'wb-quiet'));
    const ctx=canvas.getContext('2d');let width=0,height=0;
    function paint(){const dpr=devicePixelRatio||1;ctx.setTransform(dpr,0,0,dpr,0,0);ctx.clearRect(0,0,width,height);ctx.translate(w.pan.x,w.pan.y);ctx.lineWidth=3;ctx.lineCap='round';ctx.lineJoin='round';for(const s of (stroke?[...w.strokes,stroke]:w.strokes)){ctx.strokeStyle=s.color;ctx.beginPath();s.points.forEach((p,i)=>i?ctx.lineTo(...p):ctx.moveTo(...p));if(s.points.length===1){ctx.lineTo(s.points[0][0]+.1,s.points[0][1]+.1);}ctx.stroke();}canvas.style.backgroundPosition=w.pan.x+'px '+w.pan.y+'px';}
    const observer=new ResizeObserver(()=>{const r=viewport.getBoundingClientRect();width=r.width;height=r.height;const dpr=devicePixelRatio||1;canvas.width=Math.round(width*dpr);canvas.height=Math.round(height*dpr);paint();});observer.observe(viewport);widgetsCleanup.push(()=>{observer.disconnect();cancelAnimationFrame(raf);activeModules.delete(w.id);});
    const clamp=n=>Math.max(-1000000,Math.min(1000000,n));const point=e=>{const r=canvas.getBoundingClientRect();return[clamp(e.clientX-r.left-w.pan.x),clamp(e.clientY-r.top-w.pan.y)];};
    function shapePoints(a,b){const [x,y]=a,[ex,ey]=b;if(tool==='rectangle')return[a,[ex,y],b,[x,ey],a];if(tool==='ellipse'){const cx=(x+ex)/2,cy=(y+ey)/2,rx=(ex-x)/2,ry=(ey-y)/2;return Array.from({length:65},(_,i)=>[cx+rx*Math.cos(i*Math.PI/32),cy+ry*Math.sin(i*Math.PI/32)]);}if(tool==='arrow'){const angle=Math.atan2(ey-y,ex-x),length=Math.min(16,Math.hypot(ex-x,ey-y)/3);return[a,b,[ex-length*Math.cos(angle-.5),ey-length*Math.sin(angle-.5)],b,[ex-length*Math.cos(angle+.5),ey-length*Math.sin(angle+.5)]];}return[a,b];}
    canvas.onpointerdown=e=>{if(pointer!==null||(e.button!==0&&e.button!==1))return;e.preventDefault();canvas.setPointerCapture(e.pointerId);pointer=e.pointerId;activeModules.add(w.id);if(tool==='pan'||e.button===1){gesture={x:e.clientX,y:e.clientY,pan:{...w.pan}};canvas.classList.add('wb-panning');}else{strokesTotal=boards.reduce((sum,b)=>sum+b.items.reduce((n,x)=>n+(x.strokes||[]).reduce((m,s)=>m+s.points.length,0),0),0);if(strokesTotal+(tool==='draw'?1:65)>capacity.points){pointer=null;activeModules.delete(w.id);return message(t('画笔点数已达上限，请清理部分画笔内容','Drawing point limit reached. Remove some strokes.'));}shapeStart=point(e);stroke={color,points:[shapeStart]};strokesTotal++;paint();}};
    canvas.onpointermove=e=>{if(e.pointerId!==pointer)return;if(gesture){w.pan={x:clamp(gesture.pan.x+e.clientX-gesture.x),y:clamp(gesture.pan.y+e.clientY-gesture.y)};}else if(stroke&&tool!=='draw'){stroke.points=shapePoints(shapeStart,point(e)).map(p=>p.map(clamp));}else if(stroke&&strokesTotal<capacity.points){const next=point(e),last=stroke.points.at(-1);if(Math.hypot(next[0]-last[0],next[1]-last[1])<1)return;stroke.points.push(next);strokesTotal++;}else return;cancelAnimationFrame(raf);raf=requestAnimationFrame(paint);};
    canvas.onpointerup=e=>{if(e.pointerId!==pointer)return;canvas.onpointermove(e);if(stroke)w.strokes.push(stroke);if(gesture||stroke)changed();gesture=null;stroke=null;shapeStart=null;pointer=null;activeModules.delete(w.id);canvas.classList.remove('wb-panning');paint();};canvas.onpointercancel=canvas.onlostpointercapture=e=>{if(e.pointerId!==pointer)return;if(gesture){w.pan=gesture.pan;changed();}gesture=null;stroke=null;shapeStart=null;pointer=null;activeModules.delete(w.id);canvas.classList.remove('wb-panning');paint();};canvas.onkeydown=e=>{if(tool!=='pan')return;const deltas={ArrowLeft:[30,0],ArrowRight:[-30,0],ArrowUp:[0,30],ArrowDown:[0,-30]};if(!deltas[e.key])return;e.preventDefault();w.pan.x=clamp(w.pan.x+deltas[e.key][0]);w.pan.y=clamp(w.pan.y+deltas[e.key][1]);paint();changed();};
  }
  // PJAX invokes this before replacing the workspace, including browser Back/Forward.
  root.workbenchBeforeLeave=async()=>{await flush();if(dirty()){saveStatus();return false;}return true;};
  const viewportObserver=new ResizeObserver(()=>root.style.setProperty('--wb-expanded-height',Math.max(0,scroll.clientHeight-Math.min(125,Math.max(100,scroll.clientHeight*.2)))+'px'));viewportObserver.observe(scroll);
  const tick=setInterval(()=>{for(const b of boards)for(const w of b.items){if(w.type!=='timer')continue;const n=seconds(w),digits=grid.querySelector('[data-timer="'+w.id+'"]');if(digits)digits.textContent=format(n);if(w.running&&w.mode!=='0'&&n===0){w.running=false;w.remaining=0;changed();const toggle=grid.querySelector('[data-timer-toggle="'+w.id+'"]');if(toggle)toggle.lastChild.textContent=t('开始','Start');message(t('计时完成，休息一下。','Timer finished. Take a break.'));}}},250);
  const unload=e=>{if(dirty()){e.preventDefault();e.returnValue='';}};
  const navigation=e=>{const a=e.target.closest('a');if(a&&a.target!=='_blank'&&!root.contains(a)&&dirty()){e.preventDefault();e.stopImmediatePropagation();flush();message(t('正在保存，请保存完成后再离开。','Saving. Please wait before leaving.'));}};
  document.addEventListener('click',navigation,true);window.addEventListener('beforeunload',unload);
  let refreshing=false,refreshPending=false;
  async function refreshShared(){
    if(!alive)return;if(refreshing||saving){refreshPending=true;return;}refreshing=true;refreshPending=false;
    try{const response=await fetch('/resources/workbench/state',{credentials:'same-origin',signal:AbortSignal.timeout(10000)});if(response.ok){const next=await response.json();if(alive&&next.revision>=revision)syncState(next);}}
    catch{}finally{refreshing=false;if(refreshPending&&!saving)refreshShared();}
  }
  const events=new EventSource('/resources/workbench/events');
  events.addEventListener('update',refreshShared);
  events.onopen=refreshShared;
  window.addEventListener('online',refreshShared);
  const onFocusOut=()=>setTimeout(()=>{if(alive&&!saving)syncState(latest);},0);
  root.addEventListener('focusout',onFocusOut);
  const visible=()=>{if(!document.hidden)refreshShared();};document.addEventListener('visibilitychange',visible);
  setMode(false);
  return ()=>{alive=false;clearTimeout(pending);clearInterval(tick);events.close();window.removeEventListener('online',refreshShared);root.removeEventListener('focusout',onFocusOut);document.removeEventListener('visibilitychange',visible);viewportObserver.disconnect();cleanupCards();document.removeEventListener('click',navigation,true);window.removeEventListener('beforeunload',unload);delete root.workbenchBeforeLeave;root.classList.remove('wb-enhanced');};
};
