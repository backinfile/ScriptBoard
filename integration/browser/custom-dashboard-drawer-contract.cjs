const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } });
  const failures = [];
  page.on("pageerror", (error) => failures.push(error.message));
  await page.addInitScript(() => Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async (value) => { window.__copiedFieldPath = value; } } }));
  await page.route("http://dashboard.test/", (route) => route.fulfill({ contentType: "text/html", body: `<!doctype html><html><body class="custom-dashboard-admin"><details class="custom-dashboard-drawer-host" data-dashboard-drawer><summary class="button">Open</summary><div class="custom-dashboard-drawer-layer"><a href="/fallback" class="custom-dashboard-drawer-scrim" data-dashboard-drawer-close>Close</a><section class="custom-dashboard-drawer"><header class="custom-dashboard-drawer__head"><a href="/fallback" data-dashboard-drawer-close>Close</a></header><div class="custom-dashboard-drawer__body"><input aria-label="Name"></div></section></div></details><form class="custom-dashboard-card-form"><input name="value_path" value="release.version"><details data-dashboard-test-workbench><summary>Test request</summary><button type="submit" formaction="/test" data-dashboard-test-request>Run test</button><section data-dashboard-test-result hidden><header><strong data-dashboard-test-summary></strong><span data-dashboard-test-metrics></span></header><nav><button type="button" data-dashboard-test-tab="structure">Structure</button><button type="button" data-dashboard-test-tab="raw">Raw</button><button type="button" data-dashboard-test-tab="request">Request</button></nav><div data-dashboard-test-pane="structure"><input data-dashboard-json-search><div data-dashboard-json-tree></div></div><div data-dashboard-test-pane="raw" hidden><p data-dashboard-raw-note></p><pre data-dashboard-raw-response></pre></div><div data-dashboard-test-pane="request" hidden><dl data-dashboard-request-info></dl></div><div><strong data-dashboard-test-value></strong><code data-dashboard-test-type></code></div></section></details></form></body></html>` }));
	await page.route("http://dashboard.test/fallback", (route) => route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Fallback</title>" }));
	await page.route("http://dashboard.test/test", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true, diagnostic: { code: "ok", stage: "complete", url: "http://api.test/", httpStatus: 200, durationMs: 12, responseBytes: 32, contentType: "application/json" }, document: { release: { version: "v2.4.1", build: 42 } }, rawResponse: '{"release":{"version":"v2.4.1","build":42}}', value: "v2.4.1", valueType: "string", requestHeaders: { Authorization: "[REDACTED]" } }) }));
	await page.goto("http://dashboard.test/");
  const repository = path.resolve(__dirname, "../..");
  await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
  await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });
	assert.deepEqual(failures, [], `startup browser errors: ${failures.join("\n")}`);
  const drawer = page.locator("[data-dashboard-drawer]");
  const panel = page.locator(".custom-dashboard-drawer");

  await drawer.locator(":scope > summary").click();
  assert.equal(await drawer.getAttribute("open"), "", "drawer should remain open while entering");
  await page.waitForTimeout(320);
  assert.equal(await drawer.evaluate((element) => element.classList.contains("is-opening")), false, "entry class should clear after paint");
  assert.match(await panel.evaluate((element) => getComputedStyle(element).transform), /matrix\(1, 0, 0, 1, 0, 0\)|none/);

  await drawer.locator("[data-dashboard-drawer-close]").first().click();
	assert.equal(page.url(), "http://dashboard.test/", "enhanced close link must not navigate before the exit animation");
  assert.equal(await drawer.getAttribute("open"), "", "drawer must stay mounted during its exit transition");
  assert.equal(await drawer.evaluate((element) => element.classList.contains("is-closing")), true);
  await page.waitForTimeout(320);
	assert.equal(page.url(), "http://dashboard.test/", "enhanced close link must preserve the current page after closing");
  assert.equal(await drawer.getAttribute("open"), null, "drawer should close after transform transitionend");

  await drawer.locator(":scope > summary").click();
  await page.waitForTimeout(30);
  await drawer.locator("[data-dashboard-drawer-close]").first().click();
  await page.waitForTimeout(30);
  await page.evaluate(() => document.querySelector("[data-dashboard-drawer] > summary").click());
  await page.waitForTimeout(320);
  assert.equal(await drawer.getAttribute("open"), "", "rapid reopen should cancel the pending close callback");
  assert.equal(await drawer.evaluate((element) => element.classList.contains("is-closing")), false);
	await drawer.locator("[data-dashboard-drawer-close]").first().click();
	await page.waitForTimeout(320);

	const workbench = page.locator("[data-dashboard-test-workbench]");
	assert.equal(await workbench.getAttribute("open"), null, "test request workbench should be collapsed by default");
	await workbench.locator(":scope > summary").click();
	await page.locator("[data-dashboard-test-request]").click();
	const result = page.locator("[data-dashboard-test-result]");
	const copyField = result.getByRole("button", { name: "Copy field path: release.version" });
	await copyField.waitFor();
	assert.equal(await result.locator("[data-dashboard-test-summary]").textContent(), "Request succeeded");
	assert.equal(await result.locator("[data-dashboard-test-value]").textContent(), "v2.4.1");
	assert.equal((await copyField.textContent()).trim(), "", "copy field control should be icon-only");
	await copyField.click();
	assert.equal(await page.evaluate(() => window.__copiedFieldPath), "release.version");
	assert.equal(await page.locator('[name="value_path"]').inputValue(), "release.version");
	await result.locator('[data-dashboard-test-tab="request"]').click();
	assert.match(await result.locator('[data-dashboard-test-pane="request"]').textContent(), /Authorization\[REDACTED\]/);
  assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
  await browser.close();
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
