const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  const failures = [];
  page.on("pageerror", (error) => failures.push(error.message));
  await page.route("http://dashboard.test/", (route) => route.fulfill({ contentType: "text/html; charset=utf-8", body: `<!doctype html><html><head><meta charset="utf-8"></head><body>
    <div data-registry-image>
      <small data-registry-time>上传时间 2026-08-18 10:30</small>
      <small data-registry-size-row>下载大小（压缩） 12.5 KiB</small>
      <button type="button" data-registry-tag data-time-text="上传时间 2026-08-18 10:30" data-size-label="12.5 KiB" aria-pressed="true">v2.5.0</button>
      <button type="button" data-registry-tag data-time-text="构建时间 2024-01-02 08:00" data-size-label="7.0 KiB" aria-pressed="false">v1.0.0</button>
    </div>
  </body></html>` }));
	await page.goto("http://dashboard.test/");
  const repository = path.resolve(__dirname, "../..");
  await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });
	assert.deepEqual(failures, [], `startup browser errors: ${failures.join("\n")}`);

  const buttons = page.locator("[data-registry-tag]");
  await buttons.nth(1).click();
  assert.equal(await page.locator("[data-registry-time]").textContent(), "构建时间 2024-01-02 08:00");
	assert.equal(await page.locator("[data-registry-size-row]").textContent(), "下载大小（压缩） 7.0 KiB");
  assert.equal(await buttons.nth(0).getAttribute("aria-pressed"), "false");
  assert.equal(await buttons.nth(1).getAttribute("aria-pressed"), "true");

  await buttons.nth(0).click();
  assert.equal(await page.locator("[data-registry-size-row]").textContent(), "下载大小（压缩） 12.5 KiB");
  assert.equal(await page.locator("[data-registry-size-row]").isVisible(), true);
  assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
  await browser.close();
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
