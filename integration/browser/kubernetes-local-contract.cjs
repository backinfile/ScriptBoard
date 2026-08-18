const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } });
  const failures = [];
  let contextMutationURL = "";
  page.on("pageerror", (error) => failures.push(error.message));
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().includes("/monitor/")) contextMutationURL = request.url();
  });
  await page.route("http://kubernetes.test/", (route) => route.fulfill({
    contentType: "text/html",
    body: `<!doctype html><html><body data-app-shell><main data-kubernetes-page><section data-kubernetes-local data-import-preview-url="/preview"><article data-kubernetes-context-row data-context-name="team/staging" data-context-cluster="cluster" data-context-user="admin" data-context-namespace="default"><button type="button" data-kubernetes-context-edit>Edit</button></article><dialog data-kubernetes-import-drawer><form data-kubernetes-import-form><label class="kubernetes-import-drop" data-kubernetes-import-drop><input type="file" name="kubeconfig" data-kubernetes-import-file><strong data-kubernetes-import-filename>Choose or drop a kubeconfig</strong></label><div data-kubernetes-import-preview hidden><span data-import-clusters></span><span data-import-users></span><span data-import-contexts></span><span data-import-conflicts></span></div><p data-kubernetes-import-error hidden></p></form></dialog><dialog data-kubernetes-context-drawer><strong data-kubernetes-context-title></strong><form method="post" action="/monitor/kubernetes/local/contexts" data-kubernetes-context-update-form><input name="context"><input name="action" value="update"><input name="cluster"><input name="user"><input name="namespace"><button type="submit">Save</button></form><form method="post" action="/monitor/kubernetes/local/contexts" data-kubernetes-context-rename-form><input name="context"><input name="action" value="rename"><input name="name"><button type="submit">Rename</button></form></dialog></section></main></body></html>`,
  }));
  await page.route("http://kubernetes.test/preview", (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ clusters: 1, users: 1, contexts: 1, conflicts: [] }),
  }));
  await page.goto("http://kubernetes.test/");
  const repository = path.resolve(__dirname, "../..");
  await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });

  const result = await page.locator(".kubernetes-import-drop").evaluate(async (dropZone) => {
    const transfer = new DataTransfer();
    transfer.items.add(new File(["apiVersion: v1"], "dropped.yaml", { type: "application/yaml" }));
    const event = new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer: transfer });
    const accepted = !dropZone.dispatchEvent(event);
    await new Promise((resolve) => setTimeout(resolve, 50));
    return {
      accepted,
      filename: dropZone.querySelector("[data-kubernetes-import-filename]").textContent,
      inputFiles: dropZone.querySelector("[data-kubernetes-import-file]").files.length,
      previewHidden: document.querySelector("[data-kubernetes-import-preview]").hidden,
    };
  });

  assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
  assert.equal(result.accepted, true, "dropping a kubeconfig should be accepted instead of navigating the browser");
  assert.equal(result.inputFiles, 1, "the dropped kubeconfig should populate the file input");
  assert.equal(result.filename, "dropped.yaml", "the dropped kubeconfig should enter the preview flow");
  assert.equal(result.previewHidden, false, "the dropped kubeconfig should render its import preview");

  await page.locator("[data-kubernetes-context-edit]").click();
  const contextForms = await page.locator("[data-kubernetes-context-drawer]").evaluate((drawer) => ({
    updateAction: drawer.querySelector("[data-kubernetes-context-update-form]").getAttribute("action"),
    updateContext: drawer.querySelector("[data-kubernetes-context-update-form] [name=context]").value,
    renameAction: drawer.querySelector("[data-kubernetes-context-rename-form]").getAttribute("action"),
    renameContext: drawer.querySelector("[data-kubernetes-context-rename-form] [name=context]").value,
  }));
  assert.deepEqual(contextForms, {
    updateAction: "/monitor/kubernetes/local/contexts",
    updateContext: "team/staging",
    renameAction: "/monitor/kubernetes/local/contexts",
    renameContext: "team/staging",
  }, "context drawer forms should submit the original context name to the stable mutation endpoint");
  await page.locator("[data-kubernetes-context-rename-form] button[type=submit]").click();
  await page.waitForTimeout(50);
  assert.equal(contextMutationURL, "http://kubernetes.test/monitor/kubernetes/local/contexts", "context rename should post to the form action URL");
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
