"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage();
    const repository = path.resolve(__dirname, "../..");
    let status = {
      state: "current",
      issueCount: 0,
      activeRuns: 1,
      activeRunId: "only-active-run",
      websiteState: "up",
      websiteDown: 0,
      websiteVerifying: 0,
      stoppedPinnedApplications: 0,
      applicationIssueCount: 0,
    };
    await page.addInitScript(() => {
      window.setInterval = callback => {
        window.__runShellStatusUpdate = callback;
        Promise.resolve().then(callback);
        return 1;
      };
    });
    await page.route("http://scriptboard.test/**", route => {
      if (new URL(route.request().url()).pathname === "/monitor/status") {
        return route.fulfill({ contentType: "application/json", body: JSON.stringify(status) });
      }
      return route.fulfill({
        contentType: "text/html",
        body: `<!doctype html><html lang="en-US"><body>
          <section data-shell-attention data-environment="Local" data-current-errors-label="Current errors" data-active-runs-label="active Runs">
            <a href="/monitor" data-shell-attention-empty hidden></a>
            <a href="/history/runs" data-shell-attention-item="runs" hidden><strong data-shell-runs-primary></strong></a>
          </section>
        </body></html>`,
      });
    });
    await page.goto("http://scriptboard.test/harness");
    await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });

    const runs = page.locator('[data-shell-attention-item="runs"]');
    await runs.waitFor({ state: "visible" });
    assert.equal(await runs.getAttribute("href"), "/history/runs/only-active-run", "one active Run must link directly to its detail page");

    status = { ...status, activeRuns: 2, activeRunId: "" };
    await page.evaluate(() => window.__runShellStatusUpdate());
    await page.waitForFunction(() => document.querySelector('[data-shell-attention-item="runs"]')?.getAttribute("href") === "/history/runs");
    assert.equal(await runs.getAttribute("href"), "/history/runs", "multiple active Runs must link to Run history");
    process.stdout.write("Shell active Run link contract passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
