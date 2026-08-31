const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const navigation = `<nav class="sidebar-nav"><a href="/monitor">Monitor</a><a href="/resources/files">Files</a></nav>`;
const shell = main => `<!doctype html><html><head><title>ScriptBoard</title><script defer src="/assets/app-v2.js"></script></head><body><aside>${navigation}</aside>${main}</body></html>`;
const files = shell(`<main class="workspace files-page">
  <details data-file-quick-access data-validation-url="/resources/files/validate" data-pins-url="/resources/files/quick-access" data-csrf-token="csrf" data-ungrouped-label="Ungrouped" data-save-failed="Save failed" data-remove-label="Remove" data-edit-label="Edit" hidden>
    <summary>Quick access <span data-file-quick-count>0</span><span data-file-quick-count-label></span></summary>
    <p data-file-quick-status hidden></p><p data-file-quick-empty></p><ul data-file-quick-list></ul>
    <span data-file-quick-one-label>folder</span><span data-file-quick-many-label>folders</span>
  </details>
  <div data-file-quick-edit-drawer aria-hidden="true"><button data-file-quick-edit-close>Close</button><aside class="file-quick-edit-drawer" tabindex="-1"><form data-file-quick-edit-form><input data-file-quick-edit-path><input data-file-quick-edit-label><select data-file-quick-edit-group></select><small data-file-quick-edit-technical></small><button type="submit">Save</button></form></aside></div>
  <div data-deferred-region>Files loaded</div>
</main>`);

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } });
  const errors = [];
  let shellRequests = 0;
  let dataRequests = 0;
  page.on("pageerror", error => errors.push(error.message));
  await page.route("http://deferred.test/assets/app-v2.js", route => route.fulfill({
    contentType: "application/javascript",
    body: fs.readFileSync(path.resolve(__dirname, "../../internal/web/ui/assets/app.js"), "utf8"),
  }));
  await page.route("http://deferred.test/resources/files/quick-access", route => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ pins: [{ path: "/automation", label: "automation", href: "/resources/files?path=%2Fautomation", kind: "directory", groupId: "operations" }], groups: [{ ID: "operations", Name: "Operations" }] }),
  }));
  await page.route(/http:\/\/deferred\.test\/resources\/files\/validate.*/, route => route.fulfill({ contentType: "application/json", body: JSON.stringify({ accessible: true }) }));
  await page.route("http://deferred.test/resources/files", async route => {
    if (route.request().headers()["x-scriptboard-data"] === "shell") {
      shellRequests++;
    } else {
      dataRequests++;
      await new Promise(resolve => setTimeout(resolve, 350));
    }
    await route.fulfill({ contentType: "text/html", body: files });
  });
  await page.route("http://deferred.test/monitor", route => route.fulfill({ contentType: "text/html", body: shell(`<main><div data-host-overview>Monitor</div></main>`) }));

  await page.goto("http://deferred.test/monitor");
  await page.locator('.sidebar-nav a[href="/resources/files"]').click();
  await page.waitForURL("http://deferred.test/resources/files");
  const quickAccess = page.locator("[data-file-quick-access]");
  await quickAccess.waitFor({ state: "visible" });
  if ((await quickAccess.getAttribute("open")) === null) await quickAccess.locator("summary").click();
  await quickAccess.locator("[data-file-quick-edit]").click();
  const drawer = page.locator("[data-file-quick-edit-drawer]");
  await page.waitForTimeout(500);

  assert.equal(shellRequests, 1, "file navigation should fetch one shell response");
  assert.equal(dataRequests, 1, "file navigation should fetch one deferred data response");
  assert.equal(await drawer.evaluate(element => element.classList.contains("is-open")), true, "deferred data completion must not close a Quick access editor opened after navigation");
  assert.deepEqual(errors, [], `browser errors: ${errors.join("\n")}`);
  await browser.close();
  process.stdout.write("Deferred Quick access contract passed.\n");
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
