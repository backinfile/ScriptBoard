const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const files = `<!doctype html><html><head><title>ScriptBoard</title><link rel="stylesheet" href="/assets/app-v2.css"><script defer src="/assets/app-v2.js"></script></head><body>
<main class="workspace files-page">
  <details data-file-quick-access data-validation-url="/resources/files/validate" data-pins-url="/resources/files/quick-access" data-csrf-token="csrf" data-ungrouped-label="Ungrouped" data-save-failed="Save failed" data-remove-label="Remove" data-edit-label="Edit">
    <summary>Quick access <span data-file-quick-count>0</span><span data-file-quick-count-label></span></summary>
    <p data-file-quick-status hidden></p><p data-file-quick-empty></p><ul data-file-quick-list></ul>
    <span data-file-quick-one-label>item</span><span data-file-quick-many-label>items</span>
  </details>
  <div data-file-quick-edit-drawer aria-hidden="true"><aside class="file-quick-edit-drawer" tabindex="-1"><form data-file-quick-edit-form><input data-file-quick-edit-path><input data-file-quick-edit-label><select data-file-quick-edit-group></select><small data-file-quick-edit-technical></small><button type="submit">Save</button></form></aside></div>
  <div data-deferred-region>Files loaded</div>
</main></body></html>`;

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage();
    page.setDefaultTimeout(3000);
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.route("http://quick.test/assets/app-v2.js", route => route.fulfill({
      contentType: "application/javascript",
      body: fs.readFileSync(path.resolve(__dirname, "../../internal/web/ui/assets/app.js"), "utf8"),
    }));
    await page.route("http://quick.test/assets/app-v2.css", route => route.fulfill({
      contentType: "text/css",
      body: fs.readFileSync(path.resolve(__dirname, "../../internal/web/ui/assets/app.css"), "utf8"),
    }));
    await page.route("http://quick.test/resources/files/quick-access", route => route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        pins: [{ path: "/automation", label: "Automation", href: "/resources/files?path=%2Fautomation", kind: "directory", groupId: "operations" }],
        groups: [{ ID: "operations", Name: "Operations" }],
      }),
    }));
    await page.route(/http:\/\/quick\.test\/resources\/files\/validate.*/, async route => {
      await new Promise(resolve => setTimeout(resolve, 500));
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ accessible: true }) });
    });
    await page.route(/http:\/\/quick\.test\/resources\/files(?:\?.*)?$/, route => route.fulfill({ contentType: "text/html", body: files }));

    await page.addInitScript(() => localStorage.setItem(
      "scriptboard.file-quick-access-groups.collapsed",
      JSON.stringify(["operations"]),
    ));
    await page.goto("http://quick.test/resources/files");
    await page.locator("[data-file-quick-access] > summary").click();
    await page.getByRole("button", { name: /Operations/ }).click();
    const link = page.locator("[data-file-quick-list] .file-quick-row > a");
    await link.waitFor({ state: "attached" });
    await link.click();
    await page.waitForURL("http://quick.test/resources/files?path=%2Fautomation", { timeout: 2000 });
    assert.deepEqual(errors, [], `browser errors: ${errors.join("\n")}`);
    process.stdout.write("Grouped Quick access navigation passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
