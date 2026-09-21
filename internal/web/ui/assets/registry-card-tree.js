(() => {
  const initialized = new WeakSet();

  function initialize(list) {
    if (initialized.has(list)) return;
    initialized.add(list);
    const key = `scriptboard.dashboard.registry.${list.dataset.registryCardTree}.collapsed`;
    let collapsed = new Set();
    try {
      const saved = JSON.parse(localStorage.getItem(key));
      if (Array.isArray(saved)) collapsed = new Set(saved.filter(value => typeof value === "string"));
    } catch (_) { /* Keep native disclosures usable when browser storage is unavailable. */ }
    const groups = new Map();
    for (const row of Array.from(list.children)) {
      if (!row.matches("[data-registry-image]")) continue;
      const parts = (row.dataset.registryPath || "").split("/");
      parts.pop();
      let parent = list;
      let prefix = "";
      for (const part of parts) {
        prefix += part + "/";
        if (!groups.has(prefix)) {
          const group = document.createElement("details");
          group.className = "custom-dashboard-registry-group";
          group.dataset.registryPrefix = prefix;
          group.open = !collapsed.has(prefix);
          const summary = document.createElement("summary");
          const label = document.createElement("span");
          label.textContent = part + "/";
          const count = document.createElement("small");
          summary.append(label, count);
          group.append(summary);
          parent.append(group);
          const groupPrefix = prefix;
          group.addEventListener("toggle", () => {
            if (!group.isConnected) return;
            if (group.open) collapsed.delete(groupPrefix); else collapsed.add(groupPrefix);
            try { localStorage.setItem(key, JSON.stringify(Array.from(collapsed))); } catch (_) {}
          });
          groups.set(prefix, {group, count, size: 0});
        }
        const entry = groups.get(prefix);
        entry.count.textContent = String(++entry.size);
        parent = entry.group;
      }
      parent.append(row);
    }
  }

  function discover(root) {
    if (!(root instanceof Element || root instanceof Document)) return;
    if (root.matches?.("[data-registry-card-tree]")) initialize(root);
    root.querySelectorAll("[data-registry-card-tree]").forEach(initialize);
  }
  // Reapply grouping after dashboard refresh and soft navigation replace the rendered list.
  new MutationObserver(records => {
    for (const record of records) for (const node of record.addedNodes) discover(node);
  }).observe(document.body, {childList: true, subtree: true});
  discover(document);
})();
