"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
    const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 900, height: 700 } });
    const repository = path.resolve(__dirname, "../..");
    const failures = [];
    page.on("pageerror", error => failures.push(error.message));
    await page.route("http://scriptboard.test/**", route => route.fulfill({
      contentType: "text/html",
      body: "<!doctype html><html lang='en-US'><body><main><form method='post' action='/delete' data-confirm='Delete this temporary key?'><button class='button button--danger' type='submit' aria-label='Delete key'>Delete</button></form></main></body></html>",
    }));
    await page.goto("http://scriptboard.test/harness");
    await page.evaluate(() => {
      window.confirm = () => { throw new Error("native confirm must not be called"); };
      window.alert = () => { throw new Error("native alert must not be called"); };
      window.prompt = () => { throw new Error("native prompt must not be called"); };
    });
    await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
    await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });
    assert.deepEqual(failures, [], `startup browser errors: ${failures.join("\n")}`);
    await page.evaluate(() => {
      window.__confirmedSubmissions = 0;
      document.addEventListener("submit", event => {
        if (event.defaultPrevented) return;
        event.preventDefault();
        window.__confirmedSubmissions += 1;
      });
    });

    const trigger = page.getByRole("button", { name: "Delete key" });
    await trigger.focus();
    await trigger.click();
    const dialog = page.getByRole("dialog", { name: "Confirm action" });
    await dialog.waitFor();
    assert.equal(await dialog.locator("#action-dialog-0-message").count(), 0, "dialog ids must not be hard-coded");
    assert.match(await dialog.textContent(), /Delete this temporary key\?/);
    assert.equal(await dialog.getByRole("button", { name: "Delete key" }).count(), 1);
    assert.equal(await dialog.evaluate(element => element.classList.contains("action-dialog--danger")), true);
    assert.equal(await page.evaluate(() => window.__confirmedSubmissions), 0);

    await dialog.getByRole("button", { name: "Cancel" }).click();
    await dialog.waitFor({ state: "detached" });
    await page.waitForTimeout(30);
    assert.equal(await trigger.evaluate(element => document.activeElement === element), true, "cancel should restore focus to the initiating control");
    assert.equal(await page.evaluate(() => window.__confirmedSubmissions), 0);

    await trigger.click();
    const confirmedDialog = page.getByRole("dialog", { name: "Confirm action" });
    await confirmedDialog.getByRole("button", { name: "Delete key" }).click();
    await confirmedDialog.waitFor({ state: "detached" });
    await page.waitForFunction(() => window.__confirmedSubmissions === 1);
    assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);

    const source = require("node:fs").readFileSync(path.join(repository, "internal/web/ui/assets/app.js"), "utf8");
    assert.doesNotMatch(source, /window\.(?:alert|confirm|prompt)\s*\(/, "application code must not invoke native browser dialogs");
    process.stdout.write("Custom dialog contract passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
