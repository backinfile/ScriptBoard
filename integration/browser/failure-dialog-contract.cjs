"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 900, height: 700 } });
    const repository = path.resolve(__dirname, "../..");
    await page.route("http://scriptboard.test/**", async route => {
      const request = route.request();
      if (request.url().endsWith("/network-fail")) {
        await route.abort("connectionfailed");
        return;
      }
      if ((request.url().endsWith("/fail") && request.method() === "POST") ||
          request.url().endsWith("/fail-navigation") || request.url().endsWith("/fail-task")) {
        await route.fulfill({
          status: 400,
          contentType: "text/html",
          body: `<!doctype html><html lang="en-US"><head><title>Failure</title></head><body><main class="error-page"><p>HTTP 400</p><h1>Operation not completed</h1><p class="page-error">The submitted content could not be processed.</p><details class="ledger-disclosure"><div class="disclosure-body">fixture validation failed</div></details></main></body></html>`,
        });
        return;
      }
      await route.fulfill({
        contentType: "text/html",
        body: `<!doctype html><html lang="en-US"><body><main data-preserved-workspace><h1>Workspace remains</h1><form method="post" action="/fail" data-async><button class="button" type="submit">Submit failing action</button></form><a href="/fail-navigation">Open failing page</a><a href="/fail-task" data-task-link>Open failing drawer</a><a href="/network-fail">Open unavailable page</a></main></body></html>`,
      });
    });
    await page.goto("http://scriptboard.test/harness");
    await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
    await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });

    const originalURL = page.url();
    await page.getByRole("button", { name: "Submit failing action" }).click();
    const dialog = page.getByRole("dialog", { name: "Operation not completed" });
    const assertContainedFailure = async () => {
      await dialog.waitFor();
      assert.equal(page.url(), originalURL, "a failure must not navigate to an error page");
      assert.equal(await page.locator("[data-preserved-workspace]").count(), 1, "the current workspace must remain mounted");
      await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
      await dialog.waitFor({ state: "detached" });
    };
    await dialog.waitFor();
    assert.equal(page.url(), originalURL, "a failed action must not navigate to an error page");
    assert.equal(await page.locator("[data-preserved-workspace]").count(), 1, "the current workspace must remain mounted");
    assert.match(await dialog.textContent(), /The submitted content could not be processed\./);
    assert.match(await dialog.textContent(), /fixture validation failed/);
    await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
    await dialog.waitFor({ state: "detached" });

    await page.getByRole("link", { name: "Open failing page" }).click();
    await assertContainedFailure();
    await page.getByRole("link", { name: "Open failing drawer" }).click();
    await assertContainedFailure();
    await page.getByRole("link", { name: "Open unavailable page" }).click();
    await assertContainedFailure();
    process.stdout.write("Failure containment contract passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
