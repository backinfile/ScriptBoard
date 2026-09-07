/* File navigation enhances the same GET forms used without JavaScript. */
window.ScriptBoardFileJump = function ({ navigate, renderIcons, makeIcon }) {
  const opener = document.querySelector('[data-file-jump-open]');
  const standalone = document.querySelector('.file-jump-page [data-file-jump-sheet]');
  let dialog, disposeSheet, opening, alive = true;
  const close = (restore = true) => {
    opening?.abort(); opening = null;
    disposeSheet?.(); disposeSheet = null;
    if (dialog) { dialog.close(); dialog.remove(); dialog = null; }
    if (restore && opener?.isConnected) requestAnimationFrame(() => opener.focus());
  };
  function enhance(sheet) {
    const en = sheet.dataset.locale === 'en-US';
    const t = (zh, english) => en ? english : zh;
    const forms = Object.fromEntries([...sheet.querySelectorAll('[data-jump-form]')].map(form => [form.dataset.jumpForm, form]));
    const tabs = [...sheet.querySelectorAll('[data-jump-tab]')];
    const search = forms.search.elements.q, address = forms.address.elements.target;
    const list = sheet.querySelector('[data-jump-results]');
    const searchStatus = sheet.querySelector('#jump-search-status'), addressStatus = sheet.querySelector('#jump-address-status');
    const go = forms.address.querySelector('button[type=submit]');
    const goLabel = go.textContent;
    const listeners = [];
    let mode = forms.address.hidden ? 'search' : 'address', timer, searching, resolving, sequence = 0, disposed = false, composing = false;
    const listen = (node, type, fn) => { node.addEventListener(type, fn); listeners.push(() => node.removeEventListener(type, fn)); };
    const emptyLabel = t('输入文件或目录名称开始搜索。', 'Enter a file or folder name to search.');
    const failureLabel = t('请求失败，请重试。', 'Request failed. Please try again.');
    function stopSearch() { clearTimeout(timer); searching?.abort(); searching = null; sequence++; forms.search.removeAttribute('aria-busy'); }
    function stopResolve() { resolving?.abort(); resolving = null; go.textContent = goLabel; go.disabled = !address.value.trim(); forms.address.removeAttribute('aria-busy'); }
    function tab(next, focus = true) {
      if (mode !== next) { stopSearch(); stopResolve(); addressStatus.textContent = ''; }
      mode = next;
      tabs.forEach(item => { const selected = item.dataset.jumpTab === mode; item.setAttribute('role', 'tab'); item.setAttribute('aria-selected', String(selected)); item.setAttribute('aria-controls', `jump-${item.dataset.jumpTab}-panel`); item.tabIndex = selected ? 0 : -1; item.removeAttribute('aria-current'); });
      Object.entries(forms).forEach(([name, form]) => { form.hidden = name !== mode; form.setAttribute('role', 'tabpanel'); form.setAttribute('aria-labelledby', `jump-${name}-tab`); });
      if (focus) (mode === 'address' ? address : search).focus();
      if (mode === 'search' && !search.value.trim()) searchStatus.textContent = emptyLabel;
    }
    tabs[0].parentElement.setAttribute('role', 'tablist');
    tabs.forEach(item => {
      listen(item, 'click', event => { event.preventDefault(); tab(item.dataset.jumpTab); if (mode === 'search') runSearch(); });
      listen(item, 'keydown', event => { if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return; event.preventDefault(); const next = event.key === 'Home' ? 0 : event.key === 'End' ? 1 : 1-tabs.indexOf(item); tab(tabs[next].dataset.jumpTab, false); tabs[next].focus(); if (mode === 'search') runSearch(); });
    });
    function formURL(form) { const url = new URL(form.action); url.search = new URLSearchParams(new FormData(form)).toString(); return url; }
    async function json(url, controller) {
      url.searchParams.set('format', 'json');
      const response = await fetch(url, { credentials:'same-origin', signal:controller.signal, headers:{Accept:'application/json'} });
      if (response.redirected && new URL(response.url).pathname === '/login') throw Error(t('登录已过期，请刷新页面后重新登录。', 'Your session expired. Refresh this page to sign in.'));
      if (!response.headers.get('content-type')?.includes('application/json')) throw Error(failureLabel);
      const result = await response.json();
      if (!response.ok || result.Error) throw Error(result.Error || failureLabel);
      return result;
    }
    async function resolve(url, status) {
      if (resolving) return;
      const controller = new AbortController(); resolving = controller;
      stopSearch(); go.disabled = true; go.textContent = t('正在跳转…','Opening…'); forms.address.setAttribute('aria-busy','true');
      status.textContent = t('正在跳转…','Opening…');
      try {
        const result = await json(url, controller);
        if (disposed || resolving !== controller) return;
        const destination = new URL(result.url, location.href);
        if (destination.origin !== location.origin || destination.pathname !== '/resources/files') throw Error(failureLabel);
        // Commit the destination page only after its full listing can be read.
        await navigate(destination.href, true, { deferredData:false, returnFocus:opener || address, signal:controller.signal });
      } catch(error) { if (!disposed && !controller.signal.aborted) status.textContent = error.message || failureLabel; }
      finally { if (resolving === controller) { stopResolve(); if (status.textContent === t('正在跳转…','Opening…')) status.textContent = ''; } }
    }
    async function runSearch() {
      stopSearch();
      if (mode !== 'search' || composing || disposed) return;
      list.replaceChildren();
      if (!search.value.trim()) { searchStatus.textContent = emptyLabel; return; }
      const controller = new AbortController(); searching = controller; const current = sequence;
      searchStatus.textContent = t('正在搜索…','Searching…'); forms.search.setAttribute('aria-busy','true');
      try {
        const result = await json(formURL(forms.search), controller);
        if (disposed || current !== sequence) return;
        for (const entry of result.Entries) {
          const li = document.createElement('li'), link = document.createElement('a'), content = document.createElement('span'), name = document.createElement('strong'), path = document.createElement('small');
          link.href = entry.url; link.dataset.jumpResult = ''; path.textContent = entry.path;
          for (const part of entry.parts) { if (part.Match) { const mark=document.createElement('mark'); mark.textContent=part.Text; name.append(mark); } else name.append(document.createTextNode(part.Text)); }
          content.append(name,path); link.append(makeIcon(entry.kind === 'directory' ? 'folder' : 'file'),content,makeIcon('arrow-right')); li.append(link); list.append(li);
        }
        searchStatus.textContent = result.Entries.length ? t(`找到 ${result.Entries.length} 项`,`Found ${result.Entries.length} ${result.Entries.length === 1 ? 'item' : 'items'}`) : t('没有找到匹配项，请更换关键词或范围。','No matches. Try another name or search scope.');
        if (result.Partial) searchStatus.textContent += t(' 仅显示部分结果：已达到搜索上限或部分目录无法读取，请缩小范围。',' Partial results: a search limit was reached or some directories could not be read. Narrow the scope.');
        renderIcons();
      } catch(error) { if (!disposed && !controller.signal.aborted) searchStatus.textContent = error.message || failureLabel; }
      finally { if (searching === controller) { searching = null; forms.search.removeAttribute('aria-busy'); } }
    }
    listen(forms.address, 'submit', event => { event.preventDefault(); if (!composing && address.value.trim()) resolve(formURL(forms.address), addressStatus); });
    listen(address, 'input', () => { stopResolve(); addressStatus.textContent = ''; });
    listen(forms.search, 'submit', event => { event.preventDefault(); if (!composing) runSearch(); });
    listen(search, 'input', () => { stopSearch(); list.replaceChildren(); searchStatus.textContent = search.value.trim() ? t('正在搜索…','Searching…') : emptyLabel; if (!composing) timer=setTimeout(runSearch,300); });
    listen(search, 'compositionstart', () => { composing=true; stopSearch(); });
    listen(search, 'compositionend', () => { composing=false; timer=setTimeout(runSearch,300); });
    listen(forms.search.elements.scope, 'change', () => { sheet.querySelector('[data-jump-all-hint]').hidden = forms.search.elements.scope.value !== 'all'; runSearch(); });
    listen(list, 'click', event => { const link=event.target.closest('[data-jump-result]'); if (!link || event.button!==0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return; event.preventDefault(); resolve(new URL(link.href),searchStatus); });
    listen(sheet, 'keydown', event => {
      // Handle Escape before a search input consumes it to clear its value.
      if (event.key === 'Escape' && dialog && !event.isComposing) { event.preventDefault(); event.stopPropagation(); close(); return; }
      if (event.isComposing || !['ArrowDown','ArrowUp'].includes(event.key) || mode!=='search' || (event.target!==search && !event.target.closest('[data-jump-result]'))) return;
      const results=[...list.querySelectorAll('a')]; if(!results.length)return;
      event.preventDefault(); const index=results.indexOf(document.activeElement), next=index+(event.key==='ArrowDown'?1:-1);
      if(next<0 && index>=0) search.focus(); else results[index<0 ? (event.key==='ArrowDown'?0:results.length-1) : Math.min(next,results.length-1)].focus();
    });
    if (dialog) sheet.querySelectorAll('[data-jump-close]').forEach(link => listen(link,'click',event=>{event.preventDefault();close();}));
    tab(mode, false); go.disabled = !address.value.trim();
    return () => { disposed=true; stopSearch(); stopResolve(); listeners.forEach(fn=>fn()); };
  }
  async function open(event) {
    if(event.button!==0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey)return;
    event.preventDefault(); if(opening || dialog)return;
    const controller = new AbortController(); opening=controller; opener.setAttribute('aria-busy','true');
    try {
      const response=await fetch(opener.href,{signal:controller.signal,credentials:'same-origin'});
      if(!response.ok)throw Error('open');
      const documentResult=new DOMParser().parseFromString(await response.text(),'text/html');
      const sheet=documentResult.querySelector('[data-file-jump-sheet]'); if(!sheet)throw Error('open');
      if(!alive || controller.signal.aborted)return;
      dialog=document.createElement('dialog'); dialog.className='action-dialog file-jump-dialog'; dialog.setAttribute('aria-labelledby','file-jump-title'); dialog.append(document.importNode(sheet,true)); document.body.append(dialog);
      dialog.addEventListener('cancel',event=>{event.preventDefault();close();});
      dialog.addEventListener('click',event=>{if(event.target===dialog)close();});
      disposeSheet=enhance(dialog.querySelector('[data-file-jump-sheet]')); renderIcons(); dialog.showModal();
      const input=dialog.querySelector('#jump-path'); input.focus(); input.select();
    } catch(error) { if(alive && !controller.signal.aborted) navigate(opener.href,true); }
    finally { if(opening===controller)opening=null; opener.removeAttribute('aria-busy'); }
  }
  opener?.addEventListener('click',open);
  if(standalone)disposeSheet=enhance(standalone);
  return () => { alive=false; opener?.removeEventListener('click',open); close(false); };
};
