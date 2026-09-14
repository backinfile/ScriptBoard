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
