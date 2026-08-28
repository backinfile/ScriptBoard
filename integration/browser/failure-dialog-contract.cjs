"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 900, height: 700 } });
    const repository = path.resolve(__dirname, "../..");
    await page.addInitScript(() => Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: async value => { window.__copiedServerError = value; } },
    }));
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
      if (request.url().endsWith("/delete-folder") && request.method() === "POST") {
        if ((request.postData() || "").includes('name="confirm_references"')) {
          await route.fulfill({ status: 303, headers: { location: "/trash" } });
          return;
        }
        await route.fulfill({
          status: 409,
          contentType: "text/html",
          body: `<!doctype html><html lang="en-US"><body><main class="workspace confirmation-page"><h1>Confirm reference impact</h1><p>Deleting this folder invalidates one Quick Run.</p><dl><div><dt>Path</dt><dd><code>C:\\scripts</code></dd></div><div><dt>Quick Runs</dt><dd>1</dd></div></dl><form method="post" action="/delete-folder" data-async><input type="hidden" name="csrf_token" value="fixture"><input type="hidden" name="path" value="C:\\scripts"><button class="button button--danger" type="submit" name="confirm_references" value="yes">Move to Trash</button></form></main></body></html>`,
        });
        return;
      }
      if (request.url().endsWith("/start-overlap") && request.method() === "POST") {
        if ((request.postData() || "").includes('name="confirm_overlap"')) {
          await route.fulfill({ status: 303, headers: { location: "/history/runs/next" } });
          return;
        }
        await route.fulfill({
          status: 409,
          contentType: "text/html",
          body: `<!doctype html><html lang="en-US"><body><main class="workspace confirmation-page"><h1>Run another instance?</h1><p>This script is already running.</p><code>C:\\scripts\\deploy.cmd</code><form method="post" action="/start-overlap"><input type="hidden" name="csrf_token" value="fixture"><input type="hidden" name="script" value="C:\\scripts\\deploy.cmd"><input type="hidden" name="arguments" value="--safe"><button class="button button--primary" type="submit" name="confirm_overlap" value="yes">Run anyway</button></form></main></body></html>`,
        });
        return;
      }
      if (request.url().endsWith("/quick-create") && request.method() === "POST") {
        await route.fulfill({
          status: 409,
          contentType: "text/html",
          body: `<!doctype html><html lang="en-US"><body><main data-task-page data-task-kind="quick-create" data-task-close-label="Close"><section class="task-sheet"><h1>Source already exists</h1><form method="post" action="/quick-create" data-async><input type="hidden" name="csrf_token" value="fixture"><button class="button button--primary" type="submit" name="conflict_action" value="rename">Rename and create</button></form></section></main></body></html>`,
        });
        return;
      }
      if (request.url().endsWith("/quick-create")) {
        await route.fulfill({
          contentType: "text/html",
          body: `<!doctype html><html lang="en-US"><body><main data-task-page data-task-kind="quick-create" data-task-close-label="Close"><section class="task-sheet"><h1>Quick Create</h1><form method="post" action="/quick-create" data-async><button class="button button--primary" type="submit">Create</button></form></section></main></body></html>`,
        });
        return;
      }
      await route.fulfill({
        contentType: "text/html",
        body: `<!doctype html><html lang="en-US"><body><main data-preserved-workspace><h1>Workspace remains</h1><form method="post" action="/fail" data-async><button class="button" type="submit">Submit failing action</button></form><form method="post" action="/delete-folder" data-async><button class="button" type="submit">Delete referenced folder</button></form><form method="post" action="/start-overlap" data-async><input type="hidden" name="script" value="C:\\scripts\\deploy.cmd"><button class="button" type="submit">Start overlapping run</button></form><a href="/fail-navigation">Open failing page</a><a href="/fail-task" data-task-link>Open failing drawer</a><a href="/network-fail">Open unavailable page</a><a href="/quick-create" data-task-link>Open Quick Create</a></main></body></html>`,
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
    const copyDetails = dialog.getByRole("button", { name: "Copy error details" });
    assert.equal((await copyDetails.textContent()).trim(), "", "copy-error-details control should be icon-only");
    await copyDetails.click();
    assert.equal(await page.evaluate(() => window.__copiedServerError), [
      "Operation not completed",
      "The submitted content could not be processed.",
      "HTTP: 400",
      "Request: POST /fail",
      "Technical details:",
      "fixture validation failed",
    ].join("\n"), "copy-error-details should include every relevant failure field");
    const selectionCopy = await page.evaluate(() => {
      const dialog = document.querySelector(".server-error-dialog");
      const selection = getSelection();
      selection.removeAllRanges();
      const range = document.createRange();
      range.selectNodeContents(dialog);
      selection.addRange(range);
      let copied = "";
      const capture = event => { copied = event.clipboardData?.getData("text/plain") || ""; };
      document.addEventListener("copy", capture);
      document.execCommand("copy");
      document.removeEventListener("copy", capture);
      selection.removeAllRanges();
      return copied;
    });
    assert.equal(selectionCopy, [
      "Operation not completed",
      "The submitted content could not be processed.",
      "HTTP: 400",
      "Request: POST /fail",
      "Technical details:",
      "fixture validation failed",
    ].join("\n"), "manual copying from the error dialog should exclude controls and icon artifacts");
    await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
    await dialog.waitFor({ state: "detached" });

    await page.evaluate(() => {
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
      window.__fallbackCopiedServerError = "";
      document.execCommand = command => {
        const control = document.activeElement;
        if (command !== "copy" || !(control instanceof HTMLTextAreaElement)) return false;
        window.__fallbackCopiedServerError = control.value.slice(control.selectionStart, control.selectionEnd);
        return true;
      };
    });
    await page.getByRole("button", { name: "Submit failing action" }).click();
    await dialog.waitFor();
    await dialog.getByRole("button", { name: "Copy error details" }).click();
    assert.equal(await page.evaluate(() => window.__fallbackCopiedServerError), [
      "Operation not completed",
      "The submitted content could not be processed.",
      "HTTP: 400",
      "Request: POST /fail",
      "Technical details:",
      "fixture validation failed",
    ].join("\n"), "the copy button should copy full error details when Async Clipboard is unavailable inside a modal dialog");
    await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
    await dialog.waitFor({ state: "detached" });

    await page.getByRole("button", { name: "Delete referenced folder" }).click();
    const confirmation = page.getByRole("dialog", { name: "Confirm reference impact" });
    await confirmation.waitFor();
    assert.equal(await page.locator(".server-error-dialog").count(), 0, "an expected 409 confirmation must not be shown as a server error");
    const confirmedRequest = page.waitForRequest(request =>
      request.url().endsWith("/delete-folder") &&
      (request.postData() || "").includes('name="confirm_references"'),
    );
    await confirmation.getByRole("button", { name: "Move to Trash" }).click();
    const confirmationRequest = await confirmedRequest;
    assert.match(confirmationRequest.postData() || "", /name="confirm_references"[\s\S]*?yes/, "folder deletion should resubmit the server-provided confirmation fields");

    await page.getByRole("button", { name: "Start overlapping run" }).click();
    const overlapConfirmation = page.getByRole("dialog", { name: "Run another instance?" });
    await overlapConfirmation.waitFor();
    assert.equal(await page.locator(".server-error-dialog").count(), 0, "an actionable overlap conflict must not be shown as a server error");
    const overlapRequest = page.waitForRequest(request =>
      request.url().endsWith("/start-overlap") &&
      (request.postData() || "").includes('name="confirm_overlap"'),
    );
    await overlapConfirmation.getByRole("button", { name: "Run anyway" }).click();
    const confirmedOverlap = await overlapRequest;
    assert.match(confirmedOverlap.postData() || "", /name="confirm_overlap"[\s\S]*?yes/, "overlap confirmation should explicitly resubmit the server-provided choice");
    assert.match(confirmedOverlap.postData() || "", /name="arguments"[\s\S]*?--safe/, "overlap confirmation should preserve the server-provided run arguments");

    await page.getByRole("link", { name: "Open failing page" }).click();
    await assertContainedFailure();
    await page.getByRole("link", { name: "Open failing drawer" }).click();
    await assertContainedFailure();
    await page.getByRole("link", { name: "Open unavailable page" }).click();
    await assertContainedFailure();
    await page.getByRole("link", { name: "Open Quick Create" }).click();
    const quickCreate = page.getByRole("dialog", { name: "Quick Create" });
    await quickCreate.getByRole("button", { name: "Create" }).click();
    const quickCreateConflict = page.getByRole("dialog", { name: "Source already exists" });
    await quickCreateConflict.getByRole("button", { name: "Rename and create" }).waitFor();
    assert.equal(await page.locator(".server-error-dialog").count(), 0, "an actionable Quick Create conflict must remain in the task drawer");
    process.stdout.write("Failure containment contract passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
