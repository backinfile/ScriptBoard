(()=>{
  let pageCleanup=()=>{};
  let navigationController;

  const sectionLinks=[
    ['/ai','AI 工作区'],
    ['/overview','概览'],['/files/','文件'],['/quick-runs','快捷执行'],['/schedules','计划'],
    ['/variables','变量'],['/runs','运行记录'],['/audit','审计'],
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

  const formatBytes=value=>{
    if(!Number.isFinite(Number(value)))return '—';
    const units=['B','KiB','MiB','GiB','TiB'];let amount=Math.max(0,Number(value)),unit=0;
    while(amount>=1024&&unit<units.length-1){amount/=1024;unit++}
    return (unit===0?Math.round(amount):amount.toFixed(1))+' '+units[unit];
  };
  const formatRate=value=>(value==null?'0 B':formatBytes(value))+'/s';
  const formatPercent=value=>Number.isFinite(Number(value))?Number(value).toFixed(1)+'%':'—';

  function chartPath(points,key,kind){
    const values=points.map(point=>point?.[kind]?.values?.[key]);
    const percent=key.endsWith('Percent');
    const finite=values.filter(Number.isFinite);
    const maximum=percent?100:Math.max(1,...points.map(point=>point?.maximum?.values?.[key]).filter(Number.isFinite));
    const gaps=points.slice(1).map((point,index)=>new Date(point.at)-new Date(points[index].at)).filter(value=>value>0);
    const expectedGap=gaps.length?Math.min(...gaps):5000;
    const maximumGap=expectedGap<30000?15000:90000;
    let path='';
    values.forEach((value,index)=>{
      if(!Number.isFinite(value))return;
      const x=points.length<2?0:index/(points.length-1)*320;
      const y=92-Math.min(1,Math.max(0,value/maximum))*86;
      const previous=index>0?values[index-1]:undefined;
      const gap=index>0?new Date(points[index].at)-new Date(points[index-1].at):0;
      path+=(Number.isFinite(previous)&&gap<=maximumGap?'L':'M')+x.toFixed(1)+' '+y.toFixed(1)+' ';
    });
    return path.trim();
  }

  function replaceRows(tbody,rows,emptyMessage){
    if(!tbody)return;
    tbody.replaceChildren();
    if(rows.length){for(const row of rows)tbody.append(row);return}
    const row=document.createElement('tr');const cell=document.createElement('td');cell.colSpan=tbody.closest('table')?.querySelectorAll('thead th').length||5;cell.textContent=emptyMessage;row.append(cell);tbody.append(row);
  }

  function cells(...values){
    const row=document.createElement('tr');
    for(const value of values){const cell=document.createElement('td');if(value instanceof Node)cell.append(value);else cell.textContent=value;row.append(cell)}
    return row;
  }

  function renderOverview(root,payload){
    const current=payload.current||{};
    const card=(name,value,detail)=>{const element=root.querySelector(`[data-metric-card="${name}"]`);if(!element)return;element.querySelector('[data-metric-value]').textContent=value;element.querySelector('[data-metric-detail]').textContent=detail};
    const cpuDetail=(payload.facts?.logicalCores||0)+' 个逻辑核心'+(current.cpu?.Load1!=null?' · Load '+Number(current.cpu.Load1).toFixed(2):'')+(payload.capabilities?.cpuIOWait&&current.cpu?' · I/O 等待 '+formatPercent(current.cpu.IOWaitPercent):'');
    const memoryDetail=current.memory?`${formatBytes(current.memory.UsedBytes)} / ${formatBytes(current.memory.TotalBytes)} · 可用 ${formatBytes(current.memory.AvailableBytes)}`+(Number(current.memory.SwapTotalBytes)>0?' · Swap '+formatBytes(current.memory.SwapUsedBytes):'')+(payload.capabilities?.committedMemory?' · 已提交 '+formatBytes(current.memory.CommittedBytes):''):'等待采集';
    card('cpu',current.cpu?formatPercent(current.cpu.UsedPercent):'采集中',cpuDetail);
    card('memory',current.memory?formatPercent(current.memory.UsedPercent):'—',memoryDetail);
    card('storage',current.storage?formatPercent(current.storage.UsedPercent):'—',current.storage?`${current.storage.Mountpoint} · 可用 ${formatBytes(current.storage.AvailableBytes)}`:'等待采集关键卷');
    const storageCard=root.querySelector('[data-metric-card="storage"]');storageCard?.classList.toggle('metric-card--danger',Number(current.storage?.AvailableBytes)<104857600);
    card('network',current.network?formatRate(current.network.ReceivedBytesPerSecond):'采集中',current.network?`接收 ${formatRate(current.network.ReceivedBytesPerSecond)} · 发送 ${formatRate(current.network.SentBytesPerSecond)}`:'需要两个样本计算速率');
    for(const chart of root.querySelectorAll('[data-metric-chart]')){
      const key=chart.dataset.metricChart;
      chart.querySelector('[data-chart-average]').setAttribute('d',chartPath(payload.series||[],key,'average'));
      chart.querySelector('[data-chart-peak]').setAttribute('d',chartPath(payload.series||[],key,'maximum'));
    }
    const freshness=root.querySelector('[data-overview-freshness]');
    if(freshness){freshness.classList.toggle('overview-freshness--stale',!!payload.stale);freshness.querySelector('span').textContent=payload.stale?'数据已过期':'实时采集中';const time=freshness.querySelector('time');if(time&&payload.collectedAt){time.dateTime=payload.collectedAt;time.textContent=new Intl.DateTimeFormat(document.documentElement.lang||navigator.language,{dateStyle:'medium',timeStyle:'medium'}).format(new Date(payload.collectedAt))}}
    const errorBox=root.querySelector('[data-overview-errors]');
    if(errorBox){const entries=Object.entries(payload.errors||{});errorBox.hidden=entries.length===0;errorBox.replaceChildren();if(entries.length){const strong=document.createElement('strong');strong.textContent='部分指标暂不可用';const list=document.createElement('ul');for(const [name,message] of entries){const item=document.createElement('li');item.textContent=`${name}：${message}`;list.append(item)}errorBox.append(strong,list)}}

    const historyIDs=kind=>new Set((payload.series||[]).flatMap(point=>Object.keys(point?.average?.[kind]||{})));
    const offlineRows=(kind,currentIDs,columnCount=5)=>[...historyIDs(kind)].filter(id=>!currentIDs.has(id)).map(id=>cells(id,'当前离线',...Array(columnCount-2).fill('—')));
    const filesystems=current.filesystems||[],filesystemIDs=new Set(filesystems.map(value=>value.ID));
    replaceRows(root.querySelector('[data-filesystem-rows]'),filesystems.map(value=>cells(`${value.Mountpoint}\n${value.Device} · ${value.Type}`,(value.Roles||[]).map(role=>role==='managed'?'受管目录':'内部状态').join(' / ')||'—',formatPercent(value.UsedPercent),formatBytes(value.AvailableBytes),formatBytes(value.TotalBytes))).concat(offlineRows('filesystems',filesystemIDs)),'等待文件系统采集');
    const disks=current.disks||[],diskIDs=new Set(disks.map(value=>value.ID));
    replaceRows(root.querySelector('[data-disk-rows]'),disks.map(value=>cells(value.Name,formatRate(value.ReadBytesPerSecond),formatRate(value.WriteBytesPerSecond),Number(value.ReadOperationsPerSecond||0).toFixed(1),Number(value.WriteOperationsPerSecond||0).toFixed(1),value.ReadLatencyMS==null?'—':Number(value.ReadLatencyMS).toFixed(1)+' ms',value.WriteLatencyMS==null?'—':Number(value.WriteLatencyMS).toFixed(1)+' ms')).concat(offlineRows('disks',diskIDs,7)),'需要两个样本计算磁盘速率');
    const interfaces=current.interfaces||[],interfaceIDs=new Set(interfaces.map(value=>value.ID));
    replaceRows(root.querySelector('[data-network-rows]'),interfaces.map(value=>cells(value.Name,(value.Addresses||[]).join(' · ')||'—',formatRate(value.ReceivedBytesPerSecond),formatRate(value.SentBytesPerSecond),`${value.ReceivedErrors||0} / ${value.ReceivedDrops||0}`)).concat(offlineRows('networks',interfaceIDs)),'需要两个样本计算网络速率');
    const processFacts=root.querySelector('[data-process-facts]');
    if(processFacts&&current.process){processFacts.replaceChildren();const processRows=[['CPU',formatPercent(current.process.CPUPercent)],['常驻内存',formatBytes(current.process.ResidentMemoryBytes)],['线程',String(current.process.Threads||0)],payload.capabilities?.processHandles?['句柄',String(current.process.Handles||0)]:['打开文件',String(current.process.OpenFiles||0)]];for(const [label,value] of processRows){const item=document.createElement('div'),term=document.createElement('dt'),detail=document.createElement('dd');term.textContent=label;detail.textContent=value;item.append(term,detail);processFacts.append(item)}}
    const runList=root.querySelector('[data-active-runs]');
    if(runList){runList.replaceChildren();for(const run of payload.activeRuns||[]){const item=document.createElement('li'),link=document.createElement('a'),meta=document.createElement('span');link.href='/runs/'+encodeURIComponent(run.id);link.textContent=run.scriptPath;meta.textContent=run.status+' · '+new Intl.DateTimeFormat(document.documentElement.lang||navigator.language,{dateStyle:'medium',timeStyle:'medium'}).format(new Date(run.startedAt));item.append(link,meta);runList.append(item)}if(!runList.children.length){const item=document.createElement('li');item.className='active-run-list__empty';item.textContent='当前没有活动 Run';runList.append(item)}}
  }

  function initOverview(root){
    let timer,controller,range=root.dataset.range||'1h',stopped=false;
    const refresh=async()=>{
      if(stopped||document.hidden)return;
      controller?.abort();controller=new AbortController();
      try{const response=await fetch('/overview/data?range='+encodeURIComponent(range),{headers:{Accept:'application/json'},signal:controller.signal,cache:'no-store'});if(!response.ok)throw new Error('overview request failed');renderOverview(root,await response.json())}catch(error){if(error.name!=='AbortError'){const freshness=root.querySelector('[data-overview-freshness]');freshness?.classList.add('overview-freshness--stale');const label=freshness?.querySelector('span');if(label)label.textContent='数据更新失败'}}
      if(!stopped)timer=setTimeout(refresh,5000);
    };
    const visibility=()=>{clearTimeout(timer);if(!document.hidden)refresh()};
    document.addEventListener('visibilitychange',visibility);
    for(const link of root.querySelectorAll('[data-overview-range]'))link.addEventListener('click',event=>{event.preventDefault();range=link.dataset.overviewRange;root.dataset.range=range;for(const item of root.querySelectorAll('[data-overview-range]')){if(item===link)item.setAttribute('aria-current','page');else item.removeAttribute('aria-current')}const url=new URL(location.href);url.searchParams.set('range',range);history.pushState({},'',url);currentURL=url.href;clearTimeout(timer);refresh()});
    refresh();
    return()=>{stopped=true;clearTimeout(timer);controller?.abort();document.removeEventListener('visibilitychange',visibility)};
  }

  function revealCurrentNavigation(){
    const navigation=document.querySelector('.app-nav');
    const current=navigation?.querySelector('[aria-current="page"]');
    if(!navigation||!current)return;
    const navigationBounds=navigation.getBoundingClientRect();
    const currentBounds=current.getBoundingClientRect();
    if(currentBounds.left>=navigationBounds.left&&currentBounds.right<=navigationBounds.right)return;
    const centeredLeft=current.offsetLeft-(navigation.clientWidth-current.clientWidth)/2;
    navigation.scrollTo({left:Math.max(0,centeredLeft),behavior:'auto'});
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
    const forms=root.matches?.('form[data-submitting="true"]')?[root]:[];
    for(const form of forms.concat([...root.querySelectorAll('form[data-submitting="true"]')])){
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
    if((path.startsWith('/files')||path.startsWith('/runs')||path.startsWith('/schedules'))&&main&&!main.querySelector('[data-ai-context-entry]')){
      const heading=main.querySelector('.workspace-heading,.run-heading');
      if(heading){
        const link=document.createElement('a');
        link.className='compact-action';link.dataset.aiContextEntry='';
        link.href='/ai?context_type='+encodeURIComponent(path.split('/')[1]||'page')+'&context_id='+encodeURIComponent(path);
        link.textContent='询问 AI';
        heading.append(link);
      }
    }
    const aiConversation=main?.querySelector('[data-ai-conversation]');
    if(aiConversation&&window.EventSource){
      const live=aiConversation.querySelector('[data-ai-live]');
      const stream=new EventSource('/ai/conversations/'+encodeURIComponent(aiConversation.dataset.aiConversation)+'/events');
      stream.addEventListener('model_event',event=>{
        try{
          const value=JSON.parse(event.data);
          const delta=value.TextDelta||value.text_delta;
          if(delta&&live){live.textContent+=delta;live.className='ai-message ai-message--assistant ai-message--live'}
        }catch(_){}
      });
      for(const type of ['turn_finished','batch_finished','batch_summary_finished'])stream.addEventListener(type,()=>navigate(location.href,false));
      const previousCleanup=pageCleanup;
      pageCleanup=()=>{stream.close();previousCleanup()};
    }
    const aiBatchConversation=main?.querySelector('[data-ai-batch-conversation]');
    if(aiBatchConversation&&window.EventSource){
      const stream=new EventSource('/ai/conversations/'+encodeURIComponent(aiBatchConversation.dataset.aiBatchConversation)+'/events?after='+encodeURIComponent(aiBatchConversation.dataset.aiAfter||'0'));
      for(const type of ['batch_action','batch_finished'])stream.addEventListener(type,()=>navigate(location.href,false));
      const previousCleanup=pageCleanup;
      pageCleanup=()=>{stream.close();previousCleanup()};
    }
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
    revealCurrentNavigation();
    if(main){
      const section=sectionLinks.find(([href])=>href==='/files/'?path.startsWith('/files')||path==='/trash':path.startsWith(href));
      main.dataset.section=section?.[1]||'控制台';
    }

    const overview=document.querySelector('[data-host-overview]');
    if(overview){pageCleanup=initOverview(overview);return}

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

  async function replacePage(response,push){
    if(!response.headers.get('Content-Type')?.startsWith('text/html'))throw new Error('navigation response is not HTML');
    const parsed=new DOMParser().parseFromString(await response.text(),'text/html');
    const nextMain=parsed.querySelector('main');
    const nextHeader=parsed.querySelector('[data-pjax-nav]');
    const currentHeader=document.querySelector('[data-pjax-nav]');
    const currentMain=document.querySelector('main');
    if(!nextMain||!nextHeader||!currentHeader||!currentMain)throw new Error('navigation shell is incomplete');
    nextMain.dataset.pjax='';
    currentHeader.replaceWith(nextHeader);
    currentMain.replaceWith(nextMain);
    document.title=parsed.title;
    if(push)history.pushState({},'',response.url);
    else if(response.url!==location.href)history.replaceState({},'',response.url);
    currentURL=response.url;
    initPage();
    scrollTo({top:0,behavior:'auto'});
    nextMain.tabIndex=-1;
    nextMain.focus({preventScroll:true});
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
      if(!response.ok)throw new Error('navigation request failed');
      await replacePage(response,push);
    }catch(error){
      if(error.name==='AbortError')return;
      clearDirty();
      location.assign(destination);
    }
  }

  async function submitAsync(form){
    const method=(form.method||'get').toUpperCase();
    const formData=new FormData(form);
    let destination=new URL(form.action||location.href,location.href);
    const options={method,headers:{Accept:'text/html','X-ScriptBoard-Navigation':'pjax'}};
    if(method==='GET'){
      destination.search=new URLSearchParams(formData).toString();
    }else if(form.enctype==='multipart/form-data'){
      options.body=formData;
    }else{
      options.body=new URLSearchParams(formData);
    }
    try{
      const response=await fetch(destination,options);
      if(!response.headers.get('Content-Type')?.startsWith('text/html'))throw new Error('form response is not HTML');
      await replacePage(response,method==='GET'&&form.hasAttribute('data-async-push'));
    }catch(error){
      resetSubmitting(form);
      window.alert('操作未完成，请重试。');
    }
  }

  document.addEventListener('click',event=>{
    const passwordToggle=event.target.closest('[data-toggle-password]');
    if(passwordToggle){
      const field=passwordToggle.closest('[data-password-value],[data-password-editor]');
      const content=field?.querySelector('[data-password-content]');
      const mask=field?.querySelector('[data-password-mask]');
      if(content&&mask){
        const reveal=content.hidden;
        content.hidden=!reveal;
        mask.hidden=reveal;
        passwordToggle.textContent=reveal?'隐藏':'显示';
        passwordToggle.setAttribute('aria-expanded',String(reveal));
        field.classList.toggle('is-masked',!reveal);
        if(reveal&&content.matches('textarea,input'))content.focus();
      }
      return;
    }
    const closeButton=event.target.closest('[data-close-panel]');
    if(closeButton){
      const panel=closeButton.closest('details[open]');
      if(panel)closePanel(panel);
      return;
    }
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
    const link=event.target.closest('a[href]');
    if(link&&!event.defaultPrevented&&event.button===0&&!event.metaKey&&!event.ctrlKey&&!event.shiftKey&&!event.altKey&&!link.target&&!link.download){
      const destination=new URL(link.href,location.href);
      const nativePath=destination.pathname.startsWith('/files/download/')||destination.pathname.startsWith('/files/preview/')||destination.pathname==='/audit.csv';
      if(destination.origin===location.origin&&!link.hasAttribute('data-native')&&!nativePath&&destination.href!==location.href&&destination.hash===''){
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
    if(event.target.matches('[data-password-type]')){
      const field=event.target.form?.querySelector('[data-password-editor]');
      const content=field?.querySelector('[data-password-content]');
      const mask=field?.querySelector('[data-password-mask]');
      const toggle=field?.querySelector('[data-toggle-password]');
      if(content&&mask&&toggle){
        content.hidden=event.target.checked;
        mask.hidden=!event.target.checked;
        toggle.hidden=!event.target.checked;
        toggle.textContent='显示';
        toggle.setAttribute('aria-expanded',String(!event.target.checked));
        field.classList.toggle('is-masked',event.target.checked);
      }
    }
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
    if(form.hasAttribute('data-async')){
      event.preventDefault();
      submitAsync(form);
    }
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
