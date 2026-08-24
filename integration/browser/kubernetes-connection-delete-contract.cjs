const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1100, height: 800 } });
    const failures = [];
    page.on("pageerror", error => failures.push(error.message));
    await page.route("http://kubernetes.test/", route => route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html><body data-app-shell><main data-kubernetes-page><a href="/monitor/kubernetes/connections/k8s_one" data-kubernetes-connection-open>Edit connection</a><dialog class="kubernetes-drawer kubernetes-connection-drawer" data-kubernetes-connection-drawer><header><h2 data-kubernetes-connection-drawer-title>Connection settings</h2></header><div data-kubernetes-connection-drawer-body></div></dialog></main></body></html>`,
    }));
    await page.route("http://kubernetes.test/monitor/kubernetes/connections/k8s_one", route => route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html><body><main data-kubernetes-connection-page><h1>Connection settings</h1><div class="kubernetes-connection-layout"><form data-kubernetes-connection-form method="post" action="/monitor/kubernetes/connections/k8s_one"><input name="name" value="production"><button type="submit">Save</button></form><aside class="kubernetes-connection-aside"><section class="kubernetes-connection-danger"><div><h3>Delete connection</h3><p>Remove the saved connection and collected history from ScriptBoard.</p></div><form method="post" action="/monitor/kubernetes/connections/k8s_one/delete" data-confirm="Delete connection production?"><input type="hidden" name="confirm" value="yes"><button class="button button--danger" type="submit">Delete connection</button></form></section></aside></div></main></body></html>`,
    }));
    await page.goto("http://kubernetes.test/");
    const repository = path.resolve(__dirname, "../..");
    await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
    await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });
    await page.locator("[data-kubernetes-connection-open]").click();
    const drawer = page.locator("[data-kubernetes-connection-drawer]");
    await assert.doesNotReject(() => drawer.waitFor({ state: "visible" }));
    const deleteForm = drawer.locator(".kubernetes-connection-danger form");
    assert.equal(await deleteForm.getAttribute("action"), "/monitor/kubernetes/connections/k8s_one/delete");
    assert.equal(await deleteForm.getAttribute("data-confirm"), "Delete connection production?");
    assert.equal(await drawer.locator(".kubernetes-connection-danger .button--danger").textContent(), "Delete connection");
    if (process.env.SCRIPTBOARD_KUBERNETES_SCREENSHOT) {
      await page.waitForTimeout(250);
      await page.screenshot({ path: process.env.SCRIPTBOARD_KUBERNETES_SCREENSHOT, fullPage: true });
    }
    assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
