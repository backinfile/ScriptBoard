(()=>{
  let pageCleanup=()=>{};
  let navigationController;

  const sectionLinks=[
    ['/files/','文件'],['/runs','运行记录'],['/quick-runs','快捷执行'],
    ['/schedules','计划'],['/variables','变量'],['/audit','审计'],
    ['/settings/version-protection','版本保护'],['/settings/account','账户']
  ];

  const unsavedMessage='有尚未保存的更改，确定要放弃吗？';
  let currentURL=location.href;

  function updateLocalTimes(root=document){
    if(!window.Intl)return;
    const formatter=new Intl.DateTimeFormat(document.documentElement.lang||navigator.language,{dateStyle:'medium',timeStyle:'medium'});
    for(const element of root.querySelectorAll('time[data-local-time][datetime]')){
      const value=new Date(element.dateTime);
      if(!Number.isNaN(value.getTime()))element.textContent=formatter.format(value);
    }
  }

  function dirtyForms(root=document){
    const forms=root.matches?.('form[data-dirty="true"]')?[root]:[];
    return forms.concat([...root.querySelectorAll('form[data-dirty="true"]')]);
  }

  function clearDirty(root=document){
    for(const form of dirtyForms(root))delete form.dataset.dirty;
  }

  function confirmDiscard(root=document){
    if(dirtyForms(root).length===0)return true;
    if(!window.confirm(unsavedMessage))return false;
    clearDirty(root);
    return true;
  }

  function closePanel(panel,restoreFocus=true){
    if(!panel.open||!confirmDiscard(panel))return false;
    panel.open=false;
    if(restoreFocus)panel.querySelector(':scope > summary')?.focus();
    return true;
  }

  function resetSubmitting(root=document){
    for(const form of root.querySelectorAll('form[data-submitting="true"]')){
      form.removeAttribute('aria-busy');
      delete form.dataset.submitting;
      for(const mirror of form.querySelectorAll('[data-submitter-mirror]'))mirror.remove();
      const submit=form.querySelector('[data-submit-original-label]');
      if(submit){
        submit.disabled=false;
        submit.textContent=submit.dataset.submitOriginalLabel;
        delete submit.dataset.submitOriginalLabel;
      }
    }
  }

  function initPage(){
    pageCleanup();
    pageCleanup=()=>{};
    const path=location.pathname;
    const main=document.querySelector('main');
    resetSubmitting();
    updateLocalTimes();
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
      if(matchMedia('(min-width: 641px) and (pointer: fine)').matches){
        (error?.hidden?form?.querySelector('[name="username"]'):form?.querySelector('[name="password"]'))?.focus();
      }
      return;
    }

    document.body.classList.remove('login-page');
    if(main){
      const section=sectionLinks.find(([href])=>href==='/files/'?path.startsWith('/files')||path==='/trash':path.startsWith(href));
      main.dataset.section=section?.[1]||'控制台';
    }

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
    pageCleanup=()=>stream.close();
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
  }

  async function navigate(destination,push){
    if(!confirmDiscard()){
      if(!push)history.pushState({},'',currentURL);
      return;
    }
    navigationController?.abort();
    navigationController=new AbortController();
    const currentMain=document.querySelector('main');
    currentMain?.setAttribute('aria-busy','true');
    try{
      const response=await fetch(destination,{
        headers:{Accept:'text/html','X-ScriptBoard-Navigation':'pjax'},
        signal:navigationController.signal
      });
      if(!response.ok||!response.headers.get('Content-Type')?.startsWith('text/html'))throw new Error('navigation response is not HTML');
      const parsed=new DOMParser().parseFromString(await response.text(),'text/html');
      const nextMain=parsed.querySelector('main');
      const nextHeader=parsed.querySelector('[data-pjax-nav]');
      const currentHeader=document.querySelector('[data-pjax-nav]');
      if(!nextMain||!nextHeader||!currentHeader)throw new Error('navigation shell is incomplete');
      nextMain.dataset.pjax='';
      currentHeader.replaceWith(nextHeader);
      currentMain.replaceWith(nextMain);
      document.title=parsed.title;
      if(push)history.pushState({},'',response.url);
      currentURL=response.url;
      initPage();
      scrollTo({top:0,behavior:'auto'});
      nextMain.tabIndex=-1;
      nextMain.focus({preventScroll:true});
    }catch(error){
      if(error.name==='AbortError')return;
      clearDirty();
      location.assign(destination);
    }
  }

  document.addEventListener('click',event=>{
    const summary=event.target.closest('.action-panel > summary,.row-editor > summary,.row-menu > summary');
    if(summary){
      const targetPanel=summary.parentElement;
      if(targetPanel.open){
        if(!confirmDiscard(targetPanel)){
          event.preventDefault();
          return;
        }
      }else{
        for(const openPanel of document.querySelectorAll('.action-panel[open],.row-editor[open],.row-menu[open]')){
          if(openPanel!==targetPanel&&!confirmDiscard(openPanel)){
            event.preventDefault();
            return;
          }
        }
      }
    }
    const link=event.target.closest('[data-pjax-nav] a');
    if(link&&!event.defaultPrevented&&event.button===0&&!event.metaKey&&!event.ctrlKey&&!event.shiftKey&&!event.altKey&&!link.target&&!link.download){
      const destination=new URL(link.href,location.href);
      if(destination.origin===location.origin){
        event.preventDefault();
        navigate(destination.href,true);
        return;
      }
    }
    for(const panel of document.querySelectorAll('.action-panel,.row-editor,.row-menu')){
      if(panel.open&&!panel.contains(event.target)&&!closePanel(panel)){
        event.preventDefault();
        return;
      }
    }
  });
  document.addEventListener('toggle',event=>{
    const panel=event.target.closest?.('.action-panel,.row-editor,.row-menu');
    if(!panel?.open)return;
    for(const sibling of document.querySelectorAll('.action-panel,.row-editor,.row-menu'))if(sibling!==panel&&sibling.open)closePanel(sibling,false);
    requestAnimationFrame(()=>panel.querySelector('input:not([type="hidden"]),textarea,select,button,a[href]')?.focus());
  },true);
  document.addEventListener('keydown',event=>{
    const panel=event.target.closest?.('.action-panel[open],.row-editor[open],.row-menu[open]')||document.querySelector('.action-panel[open],.row-editor[open],.row-menu[open]');
    if(event.key==='Escape'&&panel){
      if(closePanel(panel))event.preventDefault();
      return;
    }
    const modalPanel=event.target.closest?.('.action-panel[open],.row-editor[open]');
    if(event.key!=='Tab'||!modalPanel)return;
    const focusable=[...modalPanel.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]):not([type="hidden"]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])')].filter(element=>!element.hidden);
    if(focusable.length===0)return;
    const first=focusable[0];
    const last=focusable[focusable.length-1];
    if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus()}
    else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}
  });
  document.addEventListener('input',event=>{
    const form=event.target.form;
    if(form?.method.toLowerCase()==='post'&&!form.matches('[data-login-form]')&&event.target.type!=='hidden')form.dataset.dirty='true';
  });
  document.addEventListener('change',event=>{
    const form=event.target.form;
    if(form?.method.toLowerCase()==='post'&&!form.matches('[data-login-form]')&&event.target.type!=='hidden')form.dataset.dirty='true';
  });
  document.addEventListener('submit',event=>{
    const form=event.target;
    if(!(form instanceof HTMLFormElement)||event.defaultPrevented)return;
    if(form.dataset.confirm&&!window.confirm(form.dataset.confirm)){
      event.preventDefault();
      return;
    }
    clearDirty(form);
    form.dataset.submitting='true';
    form.setAttribute('aria-busy','true');
    const submit=event.submitter;
    if(!submit)return;
    if(submit.name){
      const mirror=document.createElement('input');
      mirror.type='hidden';mirror.name=submit.name;mirror.value=submit.value;mirror.dataset.submitterMirror='';
      form.append(mirror);
    }
    submit.dataset.submitOriginalLabel=submit.textContent;
    submit.textContent=submit.dataset.pendingLabel||'处理中…';
    submit.disabled=true;
  });
  addEventListener('beforeunload',event=>{
    if(dirtyForms().length===0)return;
    event.preventDefault();
    event.returnValue='';
  });
  addEventListener('pageshow',()=>resetSubmitting());
  addEventListener('popstate',()=>navigate(location.href,false));
  initPage();
})();
