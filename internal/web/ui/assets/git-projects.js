(() => {
  if (window.scriptBoardGitProjects) return;
  window.scriptBoardGitProjects = true;
  const selection = () => [...document.querySelectorAll("[data-git-select]")];
  function renderSelection() {
    const form = document.querySelector("[data-git-batch]");
    if (!form) return;
    const all = document.querySelector("[data-git-all]");
    const inputs = selection();
    const checked = inputs.filter(input => input.checked);
    if (all) {
      all.checked = checked.length > 0 && checked.length === inputs.length;
      all.indeterminate = checked.length > 0 && checked.length < inputs.length;
      all.disabled = !inputs.length;
    }
    form.hidden = checked.length === 0;
    const label = form.querySelector("[data-git-selected]");
    const text = label.dataset.selectedLabel.replace("%d", checked.length);
    if (label.textContent !== text) label.textContent = text;
    const force = form.querySelector("[data-git-force]");
    const url = new URL(force.dataset.gitForceUrl, location.href);
    checked.forEach(input => url.searchParams.append("project_id", input.value));
    force.href = url.pathname + url.search;
    inputs.forEach(input => { input.closest("tr").dataset.selected = input.checked; });
  }

  function renderCheckoutMode() {
    const mode = document.querySelector("[data-git-mode]");
    if (!mode) return;
    const existing = mode.value === "existing";
    mode.form.querySelector("[data-git-branch]").hidden = existing;
    mode.form.querySelector("[data-git-remote]").required = !existing;
  }

  document.addEventListener("change", event => {
    if (event.target.matches("[data-git-all]")) selection().forEach(input => { input.checked = event.target.checked; });
    if (event.target.matches("[data-git-all], [data-git-select]")) renderSelection();
    if (event.target.matches("[data-git-mode]")) renderCheckoutMode();
  });
  document.addEventListener("click", event => {
    if (event.target.closest("[data-git-clear]")) {
      selection().forEach(input => { input.checked = false; });
      renderSelection();
      document.querySelector("[data-git-all]")?.focus();
    }
  });

  let polling = false;
  async function refreshRun() {
    const current = document.querySelector("[data-git-live]:not([data-git-finished])");
    if (!current || polling || document.hidden) return;
    polling = true;
    try {
      const response = await fetch(current.dataset.gitLive, { credentials: "same-origin", cache: "no-store" });
      if (!response.ok || !current.isConnected) return;
      const content = new DOMParser().parseFromString(await response.text(), "text/html").querySelector("[data-git-live]");
      if (!content || !current.isConnected) return;
      const finished = content.hasAttribute("data-git-finished");
      if (current.innerHTML !== content.innerHTML) {
        current.replaceChildren(...content.childNodes);
      }
      if (finished) current.setAttribute("data-git-finished", "");
    } catch {
      // The next poll can recover from a temporary connection failure.
    } finally {
      polling = false;
    }
  }

  const initialize = () => {
    renderSelection();
    renderCheckoutMode();
  };
  initialize();
  new MutationObserver(records => {
    const insertedForm = records.some(record => [...record.addedNodes].some(node =>
      node instanceof Element && (node.matches("[data-git-table], [data-git-mode]") || node.querySelector("[data-git-table], [data-git-mode]"))
    ));
    if (insertedForm) initialize();
  }).observe(document.body, { childList: true, subtree: true });
  setInterval(refreshRun, 1400);
})();
