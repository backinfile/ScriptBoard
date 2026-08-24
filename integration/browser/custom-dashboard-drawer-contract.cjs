const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } });
  const failures = [];
  page.on("pageerror", (error) => failures.push(error.message));
  await page.addInitScript(() => Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async (value) => { window.__copiedFieldPath = value; } } }));
  await page.route("http://dashboard.test/", (route) => route.fulfill({ contentType: "text/html", body: `<!doctype html><html><body class="custom-dashboard-admin"><main class="workspace custom-dashboard-workspace"><nav class="custom-dashboard-tabs"><a href="?dashboard=first"><span>First</span><small>Private</small></a><a href="?dashboard=second" aria-current="page"><span>Second</span><small>Private</small></a></nav><div class="custom-dashboard-surface"><article class="custom-dashboard-card"><span class="custom-dashboard-card__status-badge">Failed</span><details class="custom-dashboard-drawer-host" data-dashboard-drawer><summary class="button">Open</summary><div class="custom-dashboard-drawer-layer"><a href="/fallback" class="custom-dashboard-drawer-scrim" data-dashboard-drawer-close>Close</a><section class="custom-dashboard-drawer"><header class="custom-dashboard-drawer__head"><a href="/fallback" data-dashboard-drawer-close>Close</a></header><div class="custom-dashboard-drawer__body"><input aria-label="Name"></div></section></div></details></article><article class="custom-dashboard-card custom-dashboard-card--percentage"><svg><circle class="custom-dashboard-card__quota-track"></circle></svg></article><article class="custom-dashboard-card custom-dashboard-card--registry"><span class="custom-dashboard-registry-row__mark">R</span><code>registry.example/app</code></article><form class="custom-dashboard-card-form"><input name="value_path" value="release.version"><details data-dashboard-test-workbench><summary>Test request</summary><button type="submit" formaction="/test" data-dashboard-test-request>Run test</button><section data-dashboard-test-result hidden><header><strong data-dashboard-test-summary></strong><span data-dashboard-test-metrics></span></header><nav><button type="button" data-dashboard-test-tab="structure">Structure</button><button type="button" data-dashboard-test-tab="raw">Raw</button><button type="button" data-dashboard-test-tab="request">Request</button></nav><div data-dashboard-test-pane="structure"><input data-dashboard-json-search><div data-dashboard-json-tree></div></div><div data-dashboard-test-pane="raw" hidden><p data-dashboard-raw-note></p><pre data-dashboard-raw-response></pre></div><div data-dashboard-test-pane="request" hidden><dl data-dashboard-request-info></dl></div><div><strong data-dashboard-test-value></strong><code data-dashboard-test-type></code></div></section></details></form></div></main></body></html>` }));
	await page.route("http://dashboard.test/fallback", (route) => route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Fallback</title>" }));
	await page.route("http://dashboard.test/test", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true, diagnostic: { code: "ok", stage: "complete", url: "http://api.test/", httpStatus: 200, durationMs: 12, responseBytes: 32, contentType: "application/json" }, document: { release: { version: "v2.4.1", build: 42 } }, rawResponse: '{"release":{"version":"v2.4.1","build":42}}', value: "v2.4.1", valueType: "string", requestHeaders: { Authorization: "[REDACTED]" } }) }));
	await page.goto("http://dashboard.test/");
  const repository = path.resolve(__dirname, "../..");
  await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
  await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });
	await page.waitForTimeout(250);
	assert.deepEqual(failures, [], `startup browser errors: ${failures.join("\n")}`);
	const tabStyles = await page.locator(".custom-dashboard-tabs > a").evaluateAll((tabs) => tabs.map((tab) => {
		const style = getComputedStyle(tab);
		return { backgroundColor: style.backgroundColor, color: style.color, borderRightColor: style.borderRightColor, boxShadow: style.boxShadow };
	}));
	assert.equal(tabStyles[1].backgroundColor, tabStyles[0].backgroundColor, "switching dashboards must not recolor the active tab background");
	assert.equal(tabStyles[1].color, tabStyles[0].color, "switching dashboards must not recolor the active tab text");
	assert.equal(tabStyles[1].borderRightColor, tabStyles[0].borderRightColor, "switching dashboards must not recolor the active tab border");
	assert.equal(tabStyles[1].boxShadow, "none", "switching dashboards must not add an active color marker");
	const palette = await page.evaluate(() => {
		const styles = (selector) => getComputedStyle(document.querySelector(selector));
		return {
			cardBackgrounds: Array.from(document.querySelectorAll(".custom-dashboard-card"), (card) => getComputedStyle(card).backgroundColor),
			badgeBackground: styles(".custom-dashboard-card__status-badge").backgroundColor,
			quotaTrack: styles(".custom-dashboard-card__quota-track").stroke,
			registryMark: styles(".custom-dashboard-registry-row__mark").backgroundColor,
			registryCode: styles(".custom-dashboard-card--registry code").backgroundColor,
		};
	});
	assert.deepEqual(new Set(palette.cardBackgrounds), new Set(["rgb(255, 255, 255)"]), "every dashboard card must use the shared neutral surface");
	assert.equal(palette.badgeBackground, "rgb(247, 248, 251)", "card error badges must use a neutral surface");
	assert.equal(palette.quotaTrack, "rgb(212, 216, 226)", "quota tracks must use the neutral strong rule");
	assert.equal(palette.registryMark, "rgb(247, 248, 251)", "registry marks must use a neutral surface");
	assert.equal(palette.registryCode, "rgb(247, 248, 251)", "registry code labels must use a neutral surface");
	if (process.env.SCRIPTBOARD_DASHBOARD_SCREENSHOT) {
		await page.screenshot({ path: process.env.SCRIPTBOARD_DASHBOARD_SCREENSHOT, fullPage: true });
	}
  const drawer = page.locator("[data-dashboard-drawer]");
  const panel = page.locator(".custom-dashboard-drawer");

	await page.locator(".custom-dashboard-card").first().hover();
  await drawer.locator(":scope > summary").click();
	const drawerLayerBounds = await page.locator(".custom-dashboard-drawer-layer").evaluate((element) => {
		const bounds = element.getBoundingClientRect();
		return { top: bounds.top, left: bounds.left, width: bounds.width, height: bounds.height, layoutWidth: document.documentElement.clientWidth, viewportHeight: innerHeight };
	});
	assert.ok(
		drawerLayerBounds.top === 0 && drawerLayerBounds.left === 0 &&
		drawerLayerBounds.height === drawerLayerBounds.viewportHeight &&
		drawerLayerBounds.width >= drawerLayerBounds.layoutWidth - 20,
		`dashboard drawers must use the full viewport even while an ancestor is animated or hovered: ${JSON.stringify(drawerLayerBounds)}`,
	);
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
	const publicPalette = await page.evaluate(() => {
		document.body.className = "custom-dashboard-public custom-dashboard-monitor";
		const probe = document.createElement("i");
		probe.style.cssText = "position:absolute;color:var(--canvas);background:var(--surface);border-color:var(--ink)";
		document.body.append(probe);
		const bodyStyle = getComputedStyle(document.body);
		const cardStyle = getComputedStyle(document.querySelector(".custom-dashboard-card"));
		const surfaceStyle = getComputedStyle(document.querySelector(".custom-dashboard-surface") || document.querySelector(".custom-dashboard-workspace"));
		const probeStyle = getComputedStyle(probe);
		return { body: bodyStyle.backgroundColor, canvas: probeStyle.color, ink: bodyStyle.color, projectInk: probeStyle.borderColor, card: cardStyle.backgroundColor, surface: probeStyle.backgroundColor, signal: surfaceStyle.getPropertyValue("--instrument-signal").trim(), accent: getComputedStyle(document.documentElement).getPropertyValue("--accent").trim() };
	});
	assert.equal(publicPalette.body, publicPalette.canvas, "dashboard canvas should match the project canvas");
	assert.equal(publicPalette.ink, publicPalette.projectInk, "dashboard text should match the project ink");
	assert.equal(publicPalette.card, publicPalette.surface, "dashboard cards should match project surfaces");
	assert.equal(publicPalette.signal, publicPalette.accent, "dashboard signal color should reuse the project accent");
  assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
  await browser.close();
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
