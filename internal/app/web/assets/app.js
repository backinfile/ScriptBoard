
(()=>{
  const path=location.pathname;
  const main=document.querySelector('main');
  if(path==='/login'){
    document.body.classList.add('login-page');
    const form=document.querySelector('[data-login-form]');
    const error=document.querySelector('[data-login-error]');
    const errorMessage=document.querySelector('[data-login-error-message]');
    if(form&&error&&errorMessage&&window.fetch){
      form.addEventListener('submit',async event=>{
        event.preventDefault();
        const submit=form.querySelector('[type="submit"]');
        const csrf=form.querySelector('[name="csrf_token"]');
        const password=form.querySelector('[name="password"]');
        const originalLabel=submit?.textContent||'登录';
        error.hidden=true;
        form.setAttribute('aria-busy','true');
        if(submit){submit.disabled=true;submit.textContent='登录中…'}
        try{
          const response=await fetch(form.action,{
            method:'POST',
            body:new URLSearchParams(new FormData(form)),
            headers:{Accept:'application/json'}
          });
          const payload=await response.json();
          if(response.ok&&payload.redirect){location.assign(payload.redirect);return}
          if(csrf&&payload.csrf_token)csrf.value=payload.csrf_token;
          errorMessage.textContent=payload.error||'暂时无法登录，请稍后重试';
        }catch{
          errorMessage.textContent='网络连接失败，请稍后重试';
        }
        error.hidden=false;
        password?.focus();
        form.removeAttribute('aria-busy');
        if(submit){submit.disabled=false;submit.textContent=originalLabel}
      });
    }
  }else if(main){
    const links=[
      ['/files/','文件'],['/runs','运行记录'],['/quick-runs','快捷执行'],
      ['/schedules','计划'],['/variables','变量'],['/audit','审计'],
      ['/settings/version-protection','版本保护'],['/settings/account','账户']
    ];
    const section=links.find(([href])=>href==='/files/'?path.startsWith('/files')||path==='/trash':path.startsWith(href));
    main.dataset.section=section?.[1]||'控制台';
  }
  const actionPanels=[...document.querySelectorAll('.action-panel')];
  for(const panel of actionPanels){
    panel.addEventListener('toggle',()=>{
      if(!panel.open)return;
      for(const sibling of actionPanels){if(sibling!==panel)sibling.open=false}
      panel.querySelector('input:not([type="hidden"])')?.focus();
    });
  }
  document.addEventListener('click',event=>{
    for(const panel of actionPanels){if(panel.open&&!panel.contains(event.target))panel.open=false}
  });
  document.addEventListener('keydown',event=>{
    if(event.key==='Escape')for(const panel of actionPanels)panel.open=false;
  });
  const root=document.querySelector('[data-run-events-url]');
  const log=document.querySelector('[data-run-log]');
  if(!root||!log||!window.EventSource)return;
  const pause=document.querySelector('[data-run-pause]');
  const state=document.querySelector('[data-run-live-state]');
  const limit=2000;
  let paused=false;
  let completed='';
  let pending=[];
  const trim=()=>{while(log.children.length>limit)log.firstElementChild.remove()};
  const append=(data,sequence)=>{
    const span=document.createElement('span');
    span.dataset.sequence=sequence;
    span.dataset.source=data.source||'output';
    span.textContent=data.text||'';
    if(data.encoding_error)span.title='输出包含无效 UTF-8，已替换显示';
    log.append(span);trim();log.scrollTop=log.scrollHeight;
  };
  let last=Number(log.lastElementChild?.dataset.sequence||0);
  const url=new URL(root.dataset.runEventsUrl,location.href);
  if(last>0)url.searchParams.set('after',String(last));
  const stream=new EventSource(url);
  stream.addEventListener('open',()=>{if(state)state.textContent='实时连接已建立'});
  stream.addEventListener('error',()=>{if(state)state.textContent='连接中断，正在自动重连…'});
  stream.addEventListener('output',event=>{
    let data;try{data=JSON.parse(event.data)}catch{return}
    last=Number(event.lastEventId||last);
    if(paused){pending.push([data,last]);if(pending.length>limit)pending.shift();return}
    append(data,last);
  });
  stream.addEventListener('complete',event=>{
    completed=event.data;stream.close();
    if(state)state.textContent='Run 已结束：'+completed;
    if(pause)pause.hidden=true;
    const runStatus=document.querySelector('[data-run-status]');if(runStatus)runStatus.textContent=completed;
    const stopForm=document.querySelector('[data-run-stop-form]');if(stopForm)stopForm.hidden=true;
  });
  pause?.addEventListener('click',()=>{
    paused=!paused;pause.textContent=paused?'继续显示':'暂停显示';
    if(state)state.textContent=paused?'显示已暂停；后台仍在接收':(completed?'Run 已结束：'+completed:'实时显示中');
    if(!paused){for(const item of pending)append(item[0],item[1]);pending=[]}
  });
})();
