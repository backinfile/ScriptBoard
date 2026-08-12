"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 900, height: 700 } });
  page.setDefaultTimeout(3000);
  const failures = [];
  page.on("pageerror", error => failures.push(error.message));
  await page.route("http://clipboard.test/", route => route.fulfill({
    contentType: "text/html",
    body: `<!doctype html><html><body>
      <button type="button" hidden data-copy-value="C:\\inetpub\\wwwroot"
        data-copy-label="Copy path" data-copied-label="Path copied" data-copy-failed-label="Copy failed">
        <span data-lucide="copy" data-copy-icon aria-hidden="true"></span>
        <span data-copy-value-label>Copy path</span>
      </button>
    </body></html>`,
  }));
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
    window.__fallbackCopiedText = null;
    document.execCommand = command => {
      if (command !== "copy") return false;
      const control = document.activeElement;
      if (!(control instanceof HTMLTextAreaElement)) return false;
      window.__fallbackCopiedText = control.value.slice(control.selectionStart, control.selectionEnd);
      return true;
    };
  });
  await page.goto("http://clipboard.test/");
  const repository = path.resolve(__dirname, "../..");
  await page.addScriptTag({ path: path.join(repository, "internal/app/web/assets/app.js") });

  const copyButton = page.locator("[data-copy-value]");
  await copyButton.click();
  await page.locator('[data-copy-value][data-state="success"]').waitFor();
  assert.equal(await page.evaluate(() => window.__fallbackCopiedText), "C:\\inetpub\\wwwroot");
  assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
  await browser.close();
})().catch(error => {
  console.error(error);
  process.exit(1);
});
