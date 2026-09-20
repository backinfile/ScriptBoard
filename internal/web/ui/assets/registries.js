(() => {
  const initialized = new WeakSet();
  const states = new Map();
  const storageKey = id => `scriptboard.registry.tree.${id}`;

  function remember(tree) {
    const open = Array.from(tree.querySelectorAll("details[data-registry-namespace][open]"), node => node.dataset.registryNamespace);
    states.set(tree.dataset.registryTree, open);
    try { sessionStorage.setItem(storageKey(tree.dataset.registryTree), JSON.stringify(open)); } catch (_) {}
  }

  function initialize(tree) {
    if (initialized.has(tree)) return;
    initialized.add(tree);
    const id = tree.dataset.registryTree;
    let open = states.get(id);
    if (!open) {
      try {
        const saved = JSON.parse(sessionStorage.getItem(storageKey(id)));
        if (Array.isArray(saved) && saved.every(path => typeof path === "string")) open = saved;
      } catch (_) {}
    }
    // 筛选切换恢复已有展开状态，再次点击仅切换当前节点，子节点状态保持独立。
    if (open) {
      const expanded = new Set(open);
      tree.querySelectorAll("details[data-registry-namespace]").forEach(node => { node.open = expanded.has(node.dataset.registryNamespace); });
    }
    remember(tree);
  }

  document.addEventListener("click", event => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const tree = event.target.closest?.("[data-registry-tree]");
    if (!tree) return;
    initialize(tree);
    const summary = event.target.closest("summary");
    const link = summary?.querySelector(":scope > a") || event.target.closest("a");
    if (!link) return;
    remember(tree);
    if (link.getAttribute("aria-current") === "page") {
      event.preventDefault();
      event.stopImmediatePropagation();
      if (summary) {
        const node = summary.parentElement;
        node.open = !node.open;
        remember(tree);
      }
      return;
    }
    if (summary && !link.contains(event.target)) {
      // 展开标记首次点击也先选择节点，交给现有导航更新镜像列表。
      event.preventDefault();
      event.stopImmediatePropagation();
      link.click();
    }
  }, true);

  document.addEventListener("toggle", event => {
    if (!event.target.matches?.("details[data-registry-namespace]")) return;
    const tree = event.target.closest("[data-registry-tree]");
    if (tree?.isConnected) remember(tree);
  }, true);

  document.querySelectorAll("[data-registry-tree]").forEach(initialize);
  // 局部导航会替换工作区；在绘制前恢复新树，避免筛选触发展开动画。
  new MutationObserver(records => {
    records.forEach(record => record.addedNodes.forEach(node => {
      if (node.nodeType !== Node.ELEMENT_NODE) return;
      if (node.matches("[data-registry-tree]")) initialize(node);
      node.querySelectorAll("[data-registry-tree]").forEach(initialize);
    }));
  }).observe(document.body, { childList: true, subtree: true });
})();

(() => {
  const drafts = new Map();
  const activeReads = new Map();
  let active = 0;
  const queue = [];
  const text = (tag, value) => { const node = document.createElement(tag); node.textContent = value; return node; };
  const bytes = value => { const units = ["B", "KiB", "MiB", "GiB", "TiB"]; let i = 0; while (value >= 1024 && i < units.length - 1) { value /= 1024; i++; } return `${value.toFixed(i ? 1 : 0)} ${units[i]}`; };
  function selection(form) {
    if (!form) return;
    const inputs = [...form.querySelectorAll('input[name="repositories"]:not(:disabled)')];
    const count = inputs.filter(input => input.checked).length;
    const all = form.querySelector('[data-registry-select-all]');
    if (all) { all.checked = inputs.length > 0 && count === inputs.length; all.indeterminate = count > 0 && count < inputs.length; all.disabled = !inputs.length; }
    const counter = form.querySelector('[data-registry-selected]');
    if (counter) counter.textContent = `${counter.dataset.label} ${count}`;
    const preview = form.querySelector('[data-registry-preview]'); if (preview) preview.disabled = count === 0;
    const clear = form.querySelector('[data-registry-clear]'); if (clear) clear.disabled = count === 0;
  }
  function auth(form) {
    const anonymous = form.elements.auth_mode.value === 'anonymous';
    const credentials = form.querySelector('[data-registry-credentials]'); credentials.hidden = anonymous;
    credentials.querySelectorAll('input').forEach(input => { input.disabled = anonymous; });
    form.querySelector('[data-registry-anonymous]').hidden = !anonymous;
    // Hide an irrelevant TLS control without discarding its stored choice.
    form.querySelector('[data-registry-tls]').hidden = !form.elements.endpoint.value.trim().toLowerCase().startsWith('https:');
  }
  const draftKey = form => form.dataset.registryDraft + ':' + [...form.querySelectorAll('input[type="hidden"]:not([name="csrf_token"])')].map(input => `${input.name}=${input.value}`).join('&');
  const draftFields = form => [...form.querySelectorAll('input:not([type="password"]):not([type="hidden"]):not([name="confirmation"]),select')];
  document.addEventListener('input', event => {
    const form = event.target.closest('form');
    if (form?.matches('[data-registry-auth]')) auth(form);
    if (form?.matches('[data-registry-draft]')) drafts.set(draftKey(form), draftFields(form).map(input => ({name: input.name, value: input.value, checked: input.checked})));
  });
  document.addEventListener('change', event => {
    const form = event.target.closest('form');
    if (form?.matches('[data-registry-auth]')) auth(form);
    if (!form?.matches('[data-registry-selection]')) return;
    if (event.target.matches('[data-registry-select-all]')) form.querySelectorAll('input[name="repositories"]:not(:disabled)').forEach(input => { input.checked = event.target.checked; });
    selection(form);
  });
  document.addEventListener('submit', event => { const form = event.target; if (form.matches('[data-registry-draft]')) drafts.delete(draftKey(form)); });
  document.addEventListener('click', async event => {
    const clear = event.target.closest('[data-registry-clear]');
    if (clear) { const form = clear.closest('form'); form.querySelectorAll('input[name="repositories"]').forEach(input => {input.checked=false;}); selection(form); }
    const button = event.target.closest('[data-registry-copy]'); if (!button) return;
    const original = button.textContent; button.disabled = true;
    try {
      if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(button.dataset.registryCopy);
      else { const field=document.createElement('textarea');field.value=button.dataset.registryCopy;field.style.position='fixed';field.style.opacity='0';document.body.append(field);field.select();const ok=document.execCommand('copy');field.remove();button.focus();if(!ok)throw Error('copy'); }
      button.textContent = button.dataset.copied;
    } catch { button.textContent = button.dataset.failed; }
    button.setAttribute('aria-live','polite'); setTimeout(()=>{button.textContent=original;button.disabled=false;},2000);
  });
  async function read(row) {
    const tags=row.querySelector('[data-registry-tags]');const latest=row.querySelector('[data-registry-latest]');
    const controller=new AbortController();activeReads.set(row,controller);const timer=setTimeout(()=>controller.abort(),90000);
    try {
      const response=await fetch(row.dataset.registrySummary,{headers:{Accept:'application/json'},signal:controller.signal});
      const payload=await response.json();if(!response.ok)throw Error(payload.error || row.dataset.failed);
      const items=payload || [];if(!row.isConnected)return;
      const countNode=text('strong',String(items.length));countNode.className='registry-tag-count';countNode.dataset.label=row.dataset.tagLabel;
      tags.replaceChildren(countNode,text('small',items.length?items.slice(0,3).map(a=>a.Tag).join(', ')+(items.length>3?` +${items.length-3}`:''):row.dataset.empty));
      latest.replaceChildren();if(!items.length){latest.textContent='—';const checkbox=row.querySelector('input[name="repositories"]');if(checkbox){checkbox.checked=false;checkbox.disabled=true;}selection(row.closest('form'));return;}
      const known=items.filter(a=>a.Created&&!a.Created.startsWith('0001-')).sort((a,b)=>Date.parse(b.Created)-Date.parse(a.Created));const item=known[0]||items[0];
      latest.append(text('strong',item.Tag),text('small',`${row.dataset.createdLabel}: ${known.length?new Date(item.Created).toLocaleString(document.documentElement.lang):row.dataset.unknown}`),text('small',`${row.dataset.sizeLabel}: ${item.Size>0?bytes(item.Size):row.dataset.unknown}`));
      const digest=text('code',item.Digest.slice(0,19)+'…');digest.title=item.Digest;latest.append(digest);if(item.Platforms?.length)latest.append(text('small',item.Platforms.join(', ')));
    }catch(error){
      if(!row.isConnected)return;
      const retry=text('button',row.dataset.failed);retry.type='button';retry.className='button button--quiet button--compact';
      retry.addEventListener('click',()=>{retry.disabled=true;queue.push(row);pump();},{once:true});tags.replaceChildren(text('small',error.message),retry);
    }finally{clearTimeout(timer);activeReads.delete(row);}
  }
  function pump(){while(active<2&&queue.length){const row=queue.shift();if(!row.isConnected)continue;active++;read(row).finally(()=>{active--;pump();});}}
  const visible=new IntersectionObserver(entries=>{for(const entry of entries){if(entry.isIntersecting){visible.unobserve(entry.target);queue.push(entry.target);}}pump();},{rootMargin:'200px'});
  function discover(){
    for(const[row,controller]of activeReads)if(!row.isConnected)controller.abort();
    document.querySelectorAll('[data-registry-summary]:not([data-summary-ready])').forEach(row=>{row.dataset.summaryReady='1';visible.observe(row);});
    document.querySelectorAll('[data-registry-selection]:not([data-registry-ready])').forEach(form=>{form.dataset.registryReady='1';selection(form);});
    document.querySelectorAll('[data-registry-auth]:not([data-auth-ready])').forEach(form=>{form.dataset.authReady='1';auth(form);});
    document.querySelectorAll('[data-registry-draft]:not([data-draft-ready])').forEach(form=>{
      form.dataset.draftReady='1';const values=drafts.get(draftKey(form));
      if(values&&!form.closest('main').querySelector('[role="alert"],[role="status"]')) for(const saved of values){const input=form.elements.namedItem(saved.name);if(input){input.value=saved.value;if(input.type==='checkbox')input.checked=saved.checked;}}
      if(form.matches('[data-registry-auth]'))auth(form);
    });
  }
  new MutationObserver(discover).observe(document.body,{childList:true,subtree:true});discover();
})();
