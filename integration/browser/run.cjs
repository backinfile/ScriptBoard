"use strict";

const assert = require("node:assert/strict");
const { spawn, spawnSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const browserRoot = __dirname;
const repositoryRoot = path.resolve(browserRoot, "..", "..");
const snapshotRoot = path.join(browserRoot, "snapshots");
const resultRoot = path.join(browserRoot, "test-results");
const fixtureBinary = path.join(resultRoot, process.platform === "win32" ? "scriptboard-browser-fixture.exe" : "scriptboard-browser-fixture");

fs.mkdirSync(snapshotRoot, { recursive: true });
fs.rmSync(resultRoot, { recursive: true, force: true });
fs.mkdirSync(resultRoot, { recursive: true });

const build = spawnSync("go", ["build", "-o", fixtureBinary, "./integration/browser/fixture"], {
  cwd: repositoryRoot,
  encoding: "utf8",
});
if (build.status !== 0) {
  process.stderr.write(build.stdout || "");
  process.stderr.write(build.stderr || "");
  process.exit(build.status || 1);
}

function startFixture() {
  return new Promise((resolve, reject) => {
    const child = spawn(fixtureBinary, [], {
      cwd: repositoryRoot,
      stdio: ["ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    let output = "";
    const timeout = setTimeout(() => reject(new Error(`Fixture did not start:\n${output}`)), 30000);
    const inspect = chunk => {
      output += chunk.toString();
      const match = output.match(/READY (http:\/\/127\.0\.0\.1:\d+)/);
      if (match) {
        clearTimeout(timeout);
        resolve({ child, baseURL: match[1] });
      }
    };
    child.stdout.on("data", inspect);
    child.stderr.on("data", inspect);
    child.on("exit", code => {
      clearTimeout(timeout);
      reject(new Error(`Fixture exited with code ${code}:\n${output}`));
    });
  });
}

async function assertNoHorizontalOverflow(page, label) {
  const dimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
    offenders: [...document.querySelectorAll("body *")]
      .filter(element => {
        const bounds = element.getBoundingClientRect();
        return bounds.right > window.innerWidth + 1 || bounds.left < -1;
      })
      .slice(0, 8)
      .map(element => {
        const bounds = element.getBoundingClientRect();
        return {
          element: `${element.tagName.toLowerCase()}${element.className ? `.${String(element.className).replaceAll(" ", ".")}` : ""}`,
          left: Math.round(bounds.left),
          right: Math.round(bounds.right),
          width: Math.round(bounds.width),
        };
      }),
  }));
  assert.ok(dimensions.document <= dimensions.viewport + 1, `${label} overflows horizontally: ${JSON.stringify(dimensions)}`);
}

async function assertNoTableHorizontalScrollbar(page, label) {
  const shells = await page.locator(".table-shell").evaluateAll(elements => elements
    .filter(element => element.getClientRects().length > 0)
    .map(element => ({
      clientWidth: element.clientWidth,
      scrollWidth: element.scrollWidth,
      overflowX: getComputedStyle(element).overflowX,
    })));
  assert.ok(shells.length > 0, `${label} has no visible table shell`);
  assert.ok(
    shells.every(shell => shell.scrollWidth <= shell.clientWidth + 1),
    `${label} shows an unnecessary horizontal table scrollbar: ${JSON.stringify(shells)}`,
  );
}

async function assertTableRowsAligned(page, tableSelector, label) {
  const rows = await page.locator(`${tableSelector} tbody > tr`).evaluateAll(elements => elements
    .filter(element => element.getClientRects().length > 0)
    .map((row, rowIndex) => {
      const rowBounds = row.getBoundingClientRect();
      return {
        row: rowIndex + 1,
        height: Math.round(rowBounds.height * 100) / 100,
        cells: [...row.cells].map((cell, columnIndex) => {
          const cellBounds = cell.getBoundingClientRect();
          const primaryContent = cell.querySelector(":scope > .record-primary__content");
          return {
            column: columnIndex + 1,
            display: getComputedStyle(cell).display,
            primary: cell.classList.contains("record-primary"),
            primaryContentDisplay: primaryContent ? getComputedStyle(primaryContent).display : null,
            topDelta: Math.round((cellBounds.top - rowBounds.top) * 100) / 100,
            bottomDelta: Math.round((rowBounds.bottom - cellBounds.bottom) * 100) / 100,
          };
        }),
      };
    }));
  assert.ok(rows.length > 0, `${label} has no visible table rows`);
  assert.ok(
    rows.every(row => row.cells.every(cell => Math.abs(cell.topDelta) <= 1 && Math.abs(cell.bottomDelta) <= 1)),
    `${label} has cells that do not share their row edges: ${JSON.stringify(rows)}`,
  );
  assert.ok(
    rows.every(row => row.cells.every(cell =>
      !cell.primary || (cell.display === "table-cell" && cell.primaryContentDisplay === "flex"))),
    `${label} has a primary cell outside the table layout contract: ${JSON.stringify(rows)}`,
  );
}

async function saveSnapshot(page, name) {
  await page.screenshot({
    path: path.join(snapshotRoot, `${name}.png`),
    fullPage: true,
    animations: "disabled",
  });
}

async function createVariable(page, name, value, password = false) {
  const workspaceURL = page.url();
  await page.locator('a[href="/resources/variables/new"]').first().click();
  await page.locator('[data-task-panel] input[name="name"]').fill(name);
  assert.equal(page.url(), workspaceURL, "opening the variable task changed the workspace URL");
  await page.locator('[data-task-panel] textarea[name="value"]').fill(value);
  if (password) await page.locator('[data-task-panel] input[name="is_password"]').check();
  await page.locator('[data-task-panel] button[type="submit"]').click();
  await page.waitForURL("**/resources/variables");
  await page.locator("[data-task-panel]").waitFor({ state: "detached" });
  await page.getByText(name, { exact: true }).waitFor();
}

(async () => {
  const fixture = await startFixture();
  let browser;
  const consoleErrors = [];
  try {
    browser = await chromium.launch({ headless: true });
    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      deviceScaleFactor: 1,
      locale: "en-US",
      colorScheme: "light",
      reducedMotion: "reduce",
    });
    const page = await context.newPage();
    page.on("console", message => {
      if (message.type() === "error") consoleErrors.push(message.text());
    });
    page.on("pageerror", error => consoleErrors.push(error.message));

    await page.goto(`${fixture.baseURL}/login`);
    assert.equal(await page.getAttribute("html", "lang"), "en-US");
    await page.locator('input[name="username"]').fill("admin");
    await page.locator('input[name="password"]').fill("calibration-ledger-2026");
    await Promise.all([
      page.waitForURL("**/monitor"),
      page.locator('[data-login-form] button[type="submit"]').click(),
    ]);
    await page.locator("[data-app-shell]").waitFor();
    await assertNoHorizontalOverflow(page, "monitor");

    const status = await page.evaluate(async () => {
      const response = await fetch("/monitor/status", { cache: "no-store" });
      return { ok: response.ok, cache: response.headers.get("cache-control"), body: await response.json() };
    });
    assert.equal(status.ok, true);
    assert.equal(status.cache, "no-store");
    assert.equal(typeof status.body.activeRuns, "number");

    const markdownRequests = [];
    const recordMarkdownRequest = request => {
      const requestURL = new URL(request.url());
      if (requestURL.pathname.endsWith("markdown-it.min.js") ||
          requestURL.pathname.endsWith("purify.min.js") ||
          requestURL.pathname.includes("/assets/highlight") ||
          requestURL.hostname === "example.invalid") {
        markdownRequests.push(requestURL);
      }
    };
    page.on("request", recordMarkdownRequest);
    await page.goto(`${fixture.baseURL}/resources/files/view/documentation/recovery-checklist.md`);
    const markdownPreview = page.locator("[data-markdown-preview]");
    await markdownPreview.waitFor({ state: "visible" });
    assert.equal((await markdownPreview.locator("h1").textContent()).trim(), "Recovery checklist");
    assert.equal(await page.locator("[data-markdown-source]").isHidden(), true);
    assert.equal(await markdownPreview.locator("script").count(), 0);
    assert.match(await markdownPreview.textContent(), /alert\("fixture"\)/);
    const markdownCode = markdownPreview.locator("pre code.hljs");
    await markdownCode.waitFor();
    assert.equal(await markdownCode.getAttribute("class"), "language-powershell hljs");
    assert.match(await markdownCode.locator(".hljs-keyword").allTextContents().then(parts => parts.join(" ")), /param|if/);
    assert.equal(
      await markdownPreview.getByRole("link", { name: "Return to the fixture guide" }).getAttribute("href"),
      "/resources/files/view/README.md",
    );
    assert.equal(await markdownPreview.locator("img").count(), 0);
    assert.equal(
      await markdownPreview.locator(".markdown-external-image a").getAttribute("href"),
      "https://example.invalid/scriptboard-fixture.png",
    );
    assert.equal(markdownRequests.filter(url => url.pathname.endsWith("markdown-it.min.js")).length, 1);
    assert.equal(markdownRequests.filter(url => url.pathname.endsWith("purify.min.js")).length, 1);
    assert.equal(markdownRequests.filter(url => url.pathname.endsWith("/highlight.min.js")).length, 1);
    assert.equal(markdownRequests.filter(url => url.pathname.endsWith("/highlight-powershell.min.js")).length, 1);
    assert.equal(markdownRequests.filter(url => url.pathname.endsWith("/highlight-dos.min.js")).length, 0);
    assert.equal(markdownRequests.filter(url => url.hostname === "example.invalid").length, 0);
    await assertNoHorizontalOverflow(page, "Markdown preview");
    await saveSnapshot(page, "markdown-preview");

    await Promise.all([
      page.waitForURL("**/resources/files/"),
      page.locator('.app-sidebar a[href="/resources/files/"]').click(),
    ]);
    await Promise.all([
      page.waitForURL("**/resources/files/automation/"),
      page.locator('a[href="/resources/files/automation/"]').first().click(),
    ]);
    await Promise.all([
      page.waitForURL("**/resources/files/view/automation/weekly-system-check.ps1"),
      page.locator('a[href="/resources/files/view/automation/weekly-system-check.ps1"]').first().click(),
    ]);
    const scriptPreview = page.locator("[data-script-preview]");
    await scriptPreview.locator(".hljs-keyword").first().waitFor();
    assert.equal(await scriptPreview.getAttribute("data-highlight-language"), "powershell");
    assert.match(await scriptPreview.locator(".hljs-keyword").allTextContents().then(parts => parts.join(" ")), /param/);
    assert.equal(await scriptPreview.locator("script").count(), 0);
    page.off("request", recordMarkdownRequest);
    assert.equal(markdownRequests.filter(url => url.pathname.endsWith("/highlight.min.js")).length, 1);
    assert.equal(markdownRequests.filter(url => url.pathname.endsWith("/highlight-powershell.min.js")).length, 1);
    await assertNoHorizontalOverflow(page, "PowerShell preview");
    await saveSnapshot(page, "script-preview");

    await Promise.all([
      page.waitForURL("**/resources/files/automation/"),
      page.locator('.task-back[href="/resources/files/automation/"]').click(),
    ]);
    await page.waitForFunction(() => document.querySelector('a[href="/resources/files/run/automation/weekly-system-check.ps1"] svg'));
    const filesWorkspaceURL = page.url();
    await page.locator('a[href="/resources/files/run/automation/weekly-system-check.ps1"]').click();
    try {
      await page.locator("[data-task-panel]").waitFor();
    } catch (error) {
      const state = await page.evaluate(() => ({
        path: location.pathname,
        bodyClass: document.body.className,
        taskPage: document.querySelectorAll("[data-task-page]").length,
        taskPanel: document.querySelectorAll("[data-task-panel]").length,
      }));
      throw new Error(`Task panel did not open: ${JSON.stringify(state)}; console=${JSON.stringify(consoleErrors)}\n${error.message}`);
    }
    assert.equal(page.url(), filesWorkspaceURL, "opening the Run task changed the workspace URL");
    assert.equal(await page.locator("[data-task-panel]").getAttribute("aria-modal"), "true");
    await page.locator('[data-task-panel] input[name="arguments"]').fill("-Environment staging");
    await saveSnapshot(page, "files-run-task");
    await Promise.all([
      page.waitForURL(/\/monitor\/runs\/[^/]+$/),
      page.locator('[data-task-panel] button[type="submit"]').click(),
    ]);
    await page.locator("[data-run-log]").waitFor();
    await page.waitForFunction(() => document.querySelector("[data-run-log]")?.textContent.includes("result=passed"));
    assert.match(await page.locator("[data-run-log]").textContent(), /environment=staging/);
    await page.waitForFunction(() => document.querySelector("[data-run-live-state]")?.textContent.includes("complete"));
    assert.equal((await page.locator("[data-run-status]").textContent()).trim(), "Succeeded");
    assert.equal(await page.locator("[data-run-stop-form]").count(), 0);
    await assertNoHorizontalOverflow(page, "run detail");
    await saveSnapshot(page, "run-detail");

    await page.goto(`${fixture.baseURL}/monitor/runs`);
    await page.locator(".runs-table").waitFor();
    await assertTableRowsAligned(page, ".runs-table", "runs desktop");
    const runSearch = page.locator(".history-filter-form");
    const runDate = new Date();
    const runDateValue = [
      runDate.getFullYear(),
      String(runDate.getMonth() + 1).padStart(2, "0"),
      String(runDate.getDate()).padStart(2, "0"),
    ].join("-");
    await runSearch.locator('input[name="q"]').fill("weekly-system-check");
    await runSearch.locator('input[name="from"]').fill(runDateValue);
    await runSearch.locator('input[name="to"]').fill(runDateValue);
    await Promise.all([
      page.waitForURL(url => url.pathname === "/monitor/runs" &&
        url.searchParams.get("q") === "weekly-system-check" &&
        url.searchParams.get("from") === runDateValue &&
        url.searchParams.get("to") === runDateValue),
      runSearch.locator('button[type="submit"]').click(),
    ]);
    await page.locator(".runs-table").waitFor();
    const runFilterRows = await page.locator(".history-filter-form").locator(":scope > .history-filter-search, :scope > .history-date-range, :scope > .history-filter-actions").evaluateAll(elements =>
      elements.map(element => {
        const bounds = element.getBoundingClientRect();
        return { top: Math.round(bounds.top), bottom: Math.round(bounds.bottom) };
      }),
    );
    assert.equal(new Set(runFilterRows.map(element => element.top)).size, 1, JSON.stringify(runFilterRows));
    assert.equal(new Set(runFilterRows.map(element => element.bottom)).size, 1, JSON.stringify(runFilterRows));
    await assertNoHorizontalOverflow(page, "runs filtered desktop");
    await saveSnapshot(page, "runs-filtered");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await assertNoHorizontalOverflow(page, "runs filtered mobile");
    await saveSnapshot(page, "runs-filtered-mobile");
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(`${fixture.baseURL}/config/schedules`);
    await page.locator('a[href="/config/schedules/groups/new"]').click();
    const scheduleGroupTask = page.locator('[data-task-panel] [data-task-kind="schedule-group-new"]');
    await scheduleGroupTask.waitFor();
    await scheduleGroupTask.locator('input[name="name"]').fill("Operations");
    await scheduleGroupTask.getByRole("button", { name: "Create", exact: true }).click();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    const scheduleGroup = page.locator('[data-schedule-group][data-group-name="Operations"]');
    await scheduleGroup.waitFor();
    await page.locator('a[href="/config/schedules/new"]').first().click();
    const scheduleTask = page.locator('[data-task-panel] [data-task-kind="schedule-new"]');
    await scheduleTask.waitFor();
    await scheduleTask.locator('input[name="name"]').fill("Nightly safety check");
    await scheduleTask.locator('select[name="group_id"]').selectOption({ label: "Operations" });
    await scheduleTask.locator('input[name="script"]').fill("automation/weekly-system-check.ps1");
    await scheduleTask.locator('input[name="arguments"]').fill("-Environment production");
    await scheduleTask.locator('input[name="expression"]').fill("0 9 * * 1-5");
    await scheduleTask.locator("[data-cron-parse]").click();
    await scheduleTask.locator('[data-cron-mode="weekly"][aria-pressed="true"]').waitFor();
    assert.equal(await scheduleTask.locator('[data-cron-weekday][aria-pressed="true"]').count(), 5);
    await scheduleTask.locator('[data-cron-mode="daily"]').click();
    await scheduleTask.locator("[data-cron-guided-time-input]").fill("02:00");
    assert.equal(await scheduleTask.locator('input[name="expression"]').inputValue(), "0 2 * * *");
    await scheduleTask.locator('input[name="timeout_seconds"]').fill("90");
    await scheduleTask.getByRole("button", { name: "Create", exact: true }).click();
    await page.waitForURL("**/config/schedules");
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    await page.getByText("Nightly safety check", { exact: true }).waitFor();
    await scheduleGroup.waitFor();
    const scheduleGroupToggle = scheduleGroup.locator("[data-group-toggle]");
    await scheduleGroupToggle.click();
    assert.equal(await scheduleGroupToggle.getAttribute("aria-expanded"), "false");
    await scheduleGroupToggle.click();
    assert.equal(await scheduleGroupToggle.getAttribute("aria-expanded"), "true");
    let scheduleRecord = scheduleGroup.locator("[data-schedule-id]").filter({ hasText: "Nightly safety check" });
    await scheduleRecord.waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await scheduleRecord.waitFor();
    await assertNoHorizontalOverflow(page, "grouped schedules mobile");
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.reload();
    await scheduleRecord.waitFor();
    const scheduleID = await scheduleRecord.getAttribute("data-schedule-id");
    assert.ok(scheduleID);
    await scheduleRecord.getByRole("button", { name: "Run now", exact: true }).click();
    await page.waitForURL(/\/monitor\/runs\/[^/?]+$/);
    await page.locator('.status-chip[data-state="succeeded"]').waitFor({ timeout: 15_000 });
    await page.goto(`${fixture.baseURL}/config/schedules`);
    scheduleRecord = page.locator("[data-schedule-id]").filter({ hasText: "Nightly safety check" });
    await scheduleRecord.getByRole("link", { name: "View run history", exact: true }).click();
    await page.waitForURL(url => url.pathname === "/monitor/runs" &&
      url.searchParams.get("q") === "Nightly safety check" &&
      url.searchParams.get("schedule_id") === scheduleID &&
      url.searchParams.get("focus") === "search");
    assert.equal(await page.locator("#run-search").inputValue(), "Nightly safety check");
    assert.equal(await page.locator("#run-search").evaluate(input => document.activeElement === input), true);
    await page.getByText("Nightly safety check", { exact: true }).waitFor();

    await page.goto(`${fixture.baseURL}/resources/files/automation?q=weekly&sort=name&direction=desc`);
    const quickRunWorkspaceURL = page.url();
    const scriptRow = page.locator(".file-table tbody tr").filter({
      has: page.locator('a[href="/resources/files/run/automation/weekly-system-check.ps1"]'),
    });
    await scriptRow.locator(".action-menu summary").click();
    await scriptRow.getByRole("link", { name: "Add to Quick Runs" }).click();
    await page.locator('[data-task-panel] [data-task-kind="quick-new"]').waitFor();
    assert.equal(page.url(), quickRunWorkspaceURL, "opening the Quick Run task changed the workspace URL");
    assert.equal(await page.locator('[data-task-panel] input[name="name"]').inputValue(), "weekly-system-check");
    await page.locator('[data-task-panel] input[name="name"]').fill("Weekly safety check");
    await page.locator('[data-task-panel] input[name="arguments"]').fill("-Environment production");
    await page.locator('[data-task-panel] input[name="timeout_seconds"]').fill("90");
    await saveSnapshot(page, "files-quick-run-task");
    await page.locator('[data-task-panel] button[type="submit"]').click();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    await page.waitForURL(quickRunWorkspaceURL);
    assert.equal(page.url(), quickRunWorkspaceURL, "saving the Quick Run did not restore file-list state");
    await page.goto(`${fixture.baseURL}/config/quick-runs`);
    await page.getByText("Weekly safety check", { exact: true }).waitFor();
    await page.getByText("-Environment production", { exact: true }).waitFor();
    await page.locator('a[href="/config/quick-runs/groups/new"]').click();
    await page.locator('[data-task-panel] [data-task-kind="quick-group-new"]').waitFor();
    await page.locator('[data-task-panel] input[name="name"]').fill("Operations");
    await page.locator('[data-task-panel] button[type="submit"]').click();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    const operationsGroup = page.locator('[data-quick-run-group][data-group-name="Operations"]');
    await operationsGroup.waitFor();

    let quickRunRow = page.locator("[data-quick-run-id]").filter({ hasText: "Weekly safety check" });
    await quickRunRow.locator(".action-menu summary").click();
    await quickRunRow.getByRole("link", { name: "Move to group" }).click();
    await page.locator('[data-task-panel] [data-task-kind="quick-move-group"]').waitFor();
    await page.locator('[data-task-panel] select[name="group_id"]').selectOption({ label: "Operations" });
    await page.locator('[data-task-panel] button[type="submit"]').click();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    await operationsGroup.locator("[data-quick-run-id]").waitFor();

    const groupToggle = operationsGroup.locator("[data-group-toggle]");
    await groupToggle.click();
    assert.equal(await groupToggle.getAttribute("aria-expanded"), "false");
    assert.equal(await operationsGroup.locator("[data-group-body]").isHidden(), true);
    await page.reload();
    assert.equal(await operationsGroup.locator("[data-group-body]").isHidden(), true, "group collapse state was not restored");
    await operationsGroup.locator("[data-group-toggle]").click();

    quickRunRow = operationsGroup.locator("[data-quick-run-id]").filter({ hasText: "Weekly safety check" });
    await quickRunRow.locator(".action-menu summary").click();
    await quickRunRow.getByRole("link", { name: "Edit", exact: true }).click();
    await page.locator('[data-task-panel] [data-task-kind="quick-edit"]').waitFor();
    assert.equal(await page.locator('[data-task-panel] .field-readonly code').textContent(), "automation/weekly-system-check.ps1");
    await page.locator('[data-task-panel] input[name="name"]').fill("Weekly production check");
    await page.locator('[data-task-panel] button[type="submit"]').click();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });

    quickRunRow = operationsGroup.locator("[data-quick-run-id]").filter({ hasText: "Weekly production check" });
    await quickRunRow.locator(".action-menu summary").click();
    await quickRunRow.getByRole("link", { name: "Copy Quick Run" }).click();
    await page.locator('[data-task-panel] [data-task-kind="quick-copy"]').waitFor();
    await page.locator('[data-task-panel] input[name="name"]').fill("Weekly production copy");
    await page.locator('[data-task-panel] button[type="submit"]').click();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    await operationsGroup.getByText("Weekly production copy", { exact: true }).waitFor();

    quickRunRow = operationsGroup.locator("[data-quick-run-id]").filter({ hasText: "Weekly production check" });
    await quickRunRow.locator(".action-menu summary").click();
    await quickRunRow.locator('button[name="locked"][value="1"]').click();
    quickRunRow = operationsGroup.locator('[data-quick-run-id][data-locked="true"]').filter({ hasText: "Weekly production check" });
    await quickRunRow.waitFor();
    await quickRunRow.locator(".action-menu summary").click();
    assert.equal(await quickRunRow.locator('[data-locked-action="edit"][aria-disabled="true"]').count(), 1);
    assert.equal(await quickRunRow.locator('[data-locked-action="delete"][aria-disabled="true"]').count(), 1);
    await quickRunRow.locator(".action-menu summary").click();
    await assertNoHorizontalOverflow(page, "Quick Runs desktop");
    await saveSnapshot(page, "quick-runs");

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await page.evaluate(() => {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
      scrollTo(0, 0);
    });
    await assertNoHorizontalOverflow(page, "Quick Runs mobile");
    await saveSnapshot(page, "quick-runs-mobile");
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(`${fixture.baseURL}/monitor`);
    await page.locator("[data-host-overview]").waitFor();
    await page.waitForTimeout(250);
    await saveSnapshot(page, "monitor");

    await page.goto(`${fixture.baseURL}/resources/files/`);
    const uploadTaskRequests = [];
    const recordUploadTaskRequest = request => {
      const requestURL = new URL(request.url());
      if (request.method() === "GET" && requestURL.pathname === "/resources/files/upload") {
        uploadTaskRequests.push(request.url());
      }
    };
    page.on("request", recordUploadTaskRequest);
    await page.locator('a[href^="/resources/files/upload"]').first().evaluate(link => {
      for (let index = 0; index < 5; index += 1) {
        link.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 }));
      }
    });
    await page.locator("[data-task-panel]").waitFor();
    await page.waitForTimeout(250);
    page.off("request", recordUploadTaskRequest);
    assert.equal(uploadTaskRequests.length, 1, `Upload task fetched ${uploadTaskRequests.length} times`);
    assert.equal(page.url(), `${fixture.baseURL}/resources/files/`, "opening the Upload task changed the workspace URL");
    const taskScrim = page.locator(".task-panel-scrim");
    const taskScrimBackground = await taskScrim.evaluate(element => getComputedStyle(element).backgroundColor);
    await taskScrim.hover();
    assert.equal(
      await taskScrim.evaluate(element => getComputedStyle(element).backgroundColor),
      taskScrimBackground,
      "hovering the task scrim changed it from a translucent overlay to an opaque surface",
    );
    await page.keyboard.press("Escape");
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    await page.locator('a[href^="/resources/files/new-directory"]').click();
    await page.locator("[data-task-panel]").waitFor();
    assert.equal(page.url(), `${fixture.baseURL}/resources/files/`, "opening the New directory task changed the workspace URL");
    await page.goBack();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    assert.equal(page.url(), `${fixture.baseURL}/resources/files/`, "closing a task with Back changed the workspace URL");
    await page.goForward();
    await page.locator("[data-task-panel]").waitFor();
    assert.equal(page.url(), `${fixture.baseURL}/resources/files/`, "restoring a task with Forward changed the workspace URL");
    await page.keyboard.press("Escape");
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    const managedRootLocation = page.locator(".managed-root-location");
    assert.equal((await managedRootLocation.locator("dt").textContent()).trim(), "Managed root location");
    const managedRootPath = (await managedRootLocation.locator("code").textContent()).trim();
    assert.equal(path.isAbsolute(managedRootPath), true);
    const fileDropZone = page.locator("[data-file-drop-zone]");
    assert.equal((await fileDropZone.locator("[data-file-drop-title]").textContent()).trim(), "Drop files here to upload");
    assert.equal(await page.locator('[data-file-drop-form] input[name="path"]').inputValue(), "");
    const dropData = await page.evaluateHandle(() => {
      const transfer = new DataTransfer();
      transfer.items.add(new File(["uploaded by drag and drop"], "drag-upload.txt", { type: "text/plain" }));
      return transfer;
    });
    await fileDropZone.dispatchEvent("dragenter", { dataTransfer: dropData });
    assert.equal(await fileDropZone.getAttribute("data-state"), "active");
    assert.equal((await fileDropZone.locator("[data-file-drop-title]").textContent()).trim(), "Release to upload");
    await saveSnapshot(page, "files-drop-active");
    await Promise.all([
      page.waitForURL("**/resources/files/upload"),
      fileDropZone.dispatchEvent("drop", { dataTransfer: dropData }),
    ]);
    assert.equal((await page.locator("main h1").textContent()).trim(), "Upload results");
    await page.getByText("drag-upload.txt", { exact: true }).waitFor();
    await Promise.all([
      page.waitForURL("**/resources/files/"),
      page.getByRole("link", { name: "Back to files" }).click(),
    ]);
    await page.getByRole("link", { name: "drag-upload.txt", exact: true }).waitFor();
    await assertNoTableHorizontalScrollbar(page, "files desktop");
    await assertTableRowsAligned(page, ".file-table", "files desktop");
    const lastFileActionMenu = page.locator(".file-table tbody tr").last().locator(".action-menu");
    await lastFileActionMenu.evaluate(menu => menu.scrollIntoView({ block: "center" }));
    await lastFileActionMenu.locator("summary").click();
    const lastFileMenuMetrics = await lastFileActionMenu.evaluate(menu => {
      const panelBounds = menu.querySelector(":scope > div").getBoundingClientRect();
      const triggerBounds = menu.querySelector(":scope > summary").getBoundingClientRect();
      const probe = document.elementFromPoint(panelBounds.left + 12, panelBounds.bottom - 12);
      return {
        opensAbove: panelBounds.bottom < triggerBounds.top,
        insideViewport: panelBounds.top >= 0 && panelBounds.bottom <= window.innerHeight,
        visible: probe === menu || menu.contains(probe),
      };
    });
    assert.deepEqual(lastFileMenuMetrics, { opensAbove: true, insideViewport: true, visible: true });
    await assertNoTableHorizontalScrollbar(page, "files desktop action menu");
    await page.keyboard.press("Escape");
    await page.evaluate(() => document.activeElement?.blur());
    await saveSnapshot(page, "files");

    const fileSearch = page.locator("[data-file-search]");
    const fileSearchInput = fileSearch.locator("[data-search-input]");
    const fileSearchButton = fileSearch.locator("[data-search-submit]");
    await page.keyboard.press("/");
    assert.equal(await fileSearchInput.evaluate(input => document.activeElement === input), true);
    await page.waitForFunction(() => getComputedStyle(document.querySelector("[data-file-search] kbd")).opacity === "0");
    assert.equal(await fileSearch.locator("kbd").evaluate(key => getComputedStyle(key).opacity), "0");
    await fileSearchInput.blur();
    await page.waitForFunction(() => getComputedStyle(document.querySelector("[data-file-search] kbd")).opacity === "1");
    assert.equal(await fileSearch.locator("kbd").evaluate(key => getComputedStyle(key).opacity), "1");

    let releaseSearchRequest;
    const delayedSearchRequest = new Promise(resolve => {
      releaseSearchRequest = resolve;
    });
    await page.route("**/resources/files/?q=auto", async route => {
      await delayedSearchRequest;
      await route.continue();
    }, { times: 1 });
    await fileSearchInput.fill("auto");
    await fileSearchInput.press("Enter");
    assert.equal(await fileSearch.getAttribute("aria-busy"), "true");
    assert.equal(await fileSearchButton.isDisabled(), true);
    assert.equal((await fileSearchButton.textContent()).trim(), "Searching…");
    const searchedFilesURL = page.waitForURL(url =>
      url.pathname === "/resources/files/" && url.searchParams.get("q") === "auto");
    releaseSearchRequest();
    await searchedFilesURL;
    assert.equal(await page.locator("[data-search-input]").evaluate(input => document.activeElement === input), true);
    assert.match((await page.locator("[data-search-results]").textContent()).replace(/\s+/g, " "), /Found 1 item in this directory · auto/);
    assert.equal((await page.locator(".record-primary mark").textContent()).toLowerCase(), "auto");
    await saveSnapshot(page, "files-search-results");

    const fileSort = page.locator("[data-file-sort]");
    await fileSort.locator("summary").click();
    const sortPanelMetrics = await fileSort.locator(".file-sort-panel").evaluate(panel => {
      const bounds = panel.getBoundingClientRect();
      return {
        position: getComputedStyle(panel).position,
        insideViewport: bounds.top >= 0 && bounds.right <= window.innerWidth && bounds.bottom <= window.innerHeight,
      };
    });
    assert.deepEqual(sortPanelMetrics, { position: "absolute", insideViewport: true });
    assert.equal(await fileSort.locator(".file-sort-direction").isVisible(), false);
    await fileSort.locator('[name="sort"]').selectOption("type");
    assert.equal(await fileSort.locator(".file-sort-direction").isVisible(), true);
    await fileSort.locator('[name="direction"]').selectOption("desc");
    await Promise.all([
      page.waitForURL(url =>
        url.pathname === "/resources/files/" &&
        url.searchParams.get("q") === "auto" &&
        url.searchParams.get("sort") === "type" &&
        url.searchParams.get("direction") === "desc"),
      fileSort.locator("[data-sort-submit]").click(),
    ]);
    assert.equal(await page.locator("[data-search-input]").evaluate(input => document.activeElement === input), true);
    assert.match((await page.locator("[data-file-sort] summary").textContent()).replace(/\s+/g, " "), /Type · Descending/);

    await Promise.all([
      page.waitForURL(url =>
        url.pathname === "/resources/files/" &&
        !url.searchParams.has("q") &&
        url.searchParams.get("sort") === "type" &&
        url.searchParams.get("direction") === "desc"),
      page.locator("[data-search-clear]").click(),
    ]);
    assert.equal(await page.locator("[data-search-input]").evaluate(input => document.activeElement === input), true);
    assert.equal(await page.locator("[data-search-input]").inputValue(), "");
    assert.match(await page.getByRole("link", { name: "automation", exact: true }).getAttribute("href"), /sort=type/);

    await page.goBack();
    await page.waitForURL(url => url.searchParams.get("q") === "auto");
    await page.locator("[data-search-results]").waitFor();
    await page.goForward();
    await page.waitForURL(url => !url.searchParams.has("q") && url.searchParams.get("sort") === "type");
    await page.waitForFunction(() => {
      const input = document.querySelector("[data-search-input]");
      return input?.value === "" && !document.querySelector("[data-search-results]");
    });

    await page.locator("[data-search-input]").fill("not-present");
    await Promise.all([
      page.waitForURL(url => url.searchParams.get("q") === "not-present"),
      page.locator("[data-search-submit]").click(),
    ]);
    await page.locator("[data-no-search-results]").waitFor();
    assert.equal(await page.locator(".file-table").count(), 0);
    assert.equal(await page.locator(".pagination").count(), 0);
    await saveSnapshot(page, "files-search-empty");

    await page.goto(`${fixture.baseURL}/resources/files/`);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await assertNoHorizontalOverflow(page, "files mobile");
    const managedRootMobileMetrics = await page.locator(".managed-root-location code").evaluate(element => {
      const bounds = element.getBoundingClientRect();
      return {
        wraps: bounds.height > Number.parseFloat(getComputedStyle(element).lineHeight) * 1.5,
        fitsWidth: element.scrollWidth <= element.clientWidth,
      };
    });
    assert.deepEqual(managedRootMobileMetrics, { wraps: true, fitsWidth: true });
    const fileDropMobileMetrics = await page.locator("[data-file-drop-zone]").evaluate(element => {
      const bounds = element.getBoundingClientRect();
      const actionBounds = element.querySelector(".file-drop-action").getBoundingClientRect();
      return {
        fitsWidth: bounds.right <= window.innerWidth,
        actionHeight: Math.round(actionBounds.height),
      };
    });
    assert.deepEqual(fileDropMobileMetrics, { fitsWidth: true, actionHeight: 44 });
    const mobileSearchMetrics = await page.locator(".file-search-primary").evaluate(primary => {
      const bounds = primary.getBoundingClientRect();
      const inputBounds = primary.querySelector("input").getBoundingClientRect();
      const buttonBounds = primary.querySelector("button").getBoundingClientRect();
      return {
        fitsWidth: bounds.right <= window.innerWidth,
        sameRow: inputBounds.top < buttonBounds.bottom && buttonBounds.top < inputBounds.bottom,
        shortcutHidden: getComputedStyle(primary.querySelector("kbd")).display === "none",
      };
    });
    assert.deepEqual(mobileSearchMetrics, { fitsWidth: true, sameRow: true, shortcutHidden: true });
    const mobileSort = page.locator("[data-file-sort]");
    await mobileSort.locator("summary").click();
    assert.equal(await mobileSort.locator(".file-sort-panel").evaluate(panel => getComputedStyle(panel).position), "static");
    await page.keyboard.press("Escape");
    await page.evaluate(() => {
      document.activeElement?.blur();
      window.scrollTo(0, 0);
    });
    await page.waitForTimeout(50);
    await saveSnapshot(page, "files-mobile");
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(`${fixture.baseURL}/resources/files/`);
    const readmeRow = page.locator(".file-table tbody tr").filter({
      has: page.getByRole("link", { name: "README.md", exact: true }),
    });
    await readmeRow.locator(".action-menu summary").click();
    await readmeRow.getByRole("button", { name: "Move to trash" }).click();
    await readmeRow.waitFor({ state: "detached" });
    await page.goto(`${fixture.baseURL}/resources/trash`);
    await page.getByText("README.md", { exact: true }).waitFor();
    await assertTableRowsAligned(page, ".records-table", "trash desktop");

    await page.goto(`${fixture.baseURL}/history/audit`);
    const auditSearch = page.locator('form[data-async-push]');
    const now = new Date();
    const auditDate = [
      now.getFullYear(),
      String(now.getMonth() + 1).padStart(2, "0"),
      String(now.getDate()).padStart(2, "0"),
    ].join("-");
    await auditSearch.locator('input[name="q"]').fill("login");
    await auditSearch.locator('input[name="from"]').fill(auditDate);
    await auditSearch.locator('input[name="to"]').fill(auditDate);
    await Promise.all([
      page.waitForURL(url => url.pathname === "/history/audit" &&
        url.searchParams.get("q") === "login" &&
        url.searchParams.get("from") === auditDate &&
        url.searchParams.get("to") === auditDate),
      auditSearch.locator('button[type="submit"]').click(),
    ]);
    assert.equal(await page.locator('input[name="q"]').inputValue(), "login");
    assert.equal(await page.locator('input[name="from"]').inputValue(), auditDate);
    assert.equal(await page.locator('input[name="to"]').inputValue(), auditDate);
    const auditFilterRows = await auditSearch.locator(":scope > .history-filter-search, :scope > .history-date-range, :scope > .history-filter-actions").evaluateAll(elements =>
      elements.map(element => {
        const bounds = element.getBoundingClientRect();
        return { top: Math.round(bounds.top), bottom: Math.round(bounds.bottom) };
      }),
    );
    assert.equal(new Set(auditFilterRows.map(element => element.top)).size, 1, JSON.stringify(auditFilterRows));
    assert.equal(new Set(auditFilterRows.map(element => element.bottom)).size, 1, JSON.stringify(auditFilterRows));

    await page.goto(`${fixture.baseURL}/resources/variables`);
    await createVariable(page, "DEPLOY_REGION", "west-europe");
    await createVariable(page, "PRIMARY_TOKEN", "line-one\nline-two-with-a-long-value-that-must-not-expand-the-table", true);
    await createVariable(page, "SECONDARY_TOKEN", "second-secret", true);
    await assertNoTableHorizontalScrollbar(page, "variables desktop");
    await assertTableRowsAligned(page, ".variables-table", "variables desktop");

    const primarySecretRow = page.locator("tbody tr").filter({ hasText: "PRIMARY_TOKEN" });
    const secondarySecretRow = page.locator("tbody tr").filter({ hasText: "SECONDARY_TOKEN" });
    const primaryToggle = primarySecretRow.locator("[data-toggle-password]");
    const primaryContent = primarySecretRow.locator("[data-password-content]");
    const secondaryContent = secondarySecretRow.locator("[data-password-content]");

    assert.equal(await primarySecretRow.locator("[data-password-mask]").textContent(), "••••••••");
    assert.equal(await primaryContent.isHidden(), true);
    assert.equal(await secondaryContent.isHidden(), true);
    assert.deepEqual(
      await primarySecretRow.locator(".secret-value").evaluate(container =>
        [...container.children]
          .filter(child => child.matches("[data-password-mask], [data-password-content], .secret-controls"))
          .map(child => child.hasAttribute("data-password-mask") ? "value-mask" :
            child.hasAttribute("data-password-content") ? "value-content" : "controls")),
      ["value-mask", "value-content", "controls"],
    );
    assert.equal(await primaryToggle.getAttribute("title"), null);
    assert.equal(await primarySecretRow.locator("[data-copy-password]").getAttribute("title"), null);
    await primaryToggle.hover();
    await page.waitForFunction(() => getComputedStyle(document.querySelector("[data-toggle-password]"), "::after").opacity === "1");
    assert.deepEqual(
      await primaryToggle.evaluate(element => {
        const tooltip = getComputedStyle(element, "::after");
        return {
          content: tooltip.content.replaceAll('"', ""),
          opacity: tooltip.opacity,
          visibility: tooltip.visibility,
        };
      }),
      { content: "Show variable value", opacity: "1", visibility: "visible" },
    );
    await page.mouse.move(4, 4);

    await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: fixture.baseURL });
    const normalRow = page.locator("tbody tr").filter({ hasText: "DEPLOY_REGION" });
    await normalRow.locator("[data-copy-name]").click();
    await normalRow.locator('[data-copy-name][data-state="success"]').waitFor();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), "DEPLOY_REGION");
    await normalRow.locator("[data-copy-value]").click();
    await normalRow.locator('[data-copy-value][data-state="success"]').waitFor();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), "west-europe");

    await primarySecretRow.locator("[data-copy-password]").click();
    await primarySecretRow.locator('[data-copy-password][data-state="success"]').waitFor();
    assert.equal(await primarySecretRow.locator("[data-copy-password]").getAttribute("data-tooltip"), "Copied");
    assert.equal(
      await page.evaluate(() => navigator.clipboard.readText()),
      "line-one\r\nline-two-with-a-long-value-that-must-not-expand-the-table",
    );
    assert.equal(await primaryContent.isHidden(), true);
    await page.evaluate(() => {
      navigator.clipboard.writeText = async () => {
        throw new Error("simulated clipboard failure");
      };
    });
    await secondarySecretRow.locator("[data-copy-password]").click();
    await secondarySecretRow.locator('[data-copy-password][data-state="error"]').waitFor();
    assert.equal(await secondarySecretRow.locator("[data-copy-password]").getAttribute("data-tooltip"), "Copy failed. Select the content manually.");
    assert.match(await secondarySecretRow.locator("[data-password-status]").textContent(), /Copy failed/);
    assert.equal(await secondaryContent.isHidden(), true);

    await primaryToggle.focus();
    await page.keyboard.press("Enter");
    assert.equal(await primaryToggle.getAttribute("aria-expanded"), "true");
    assert.match(await primaryToggle.getAttribute("aria-label"), /^Hide variable value/);
    assert.equal(await primaryToggle.getAttribute("data-tooltip"), "Hide variable value");
    assert.equal(await primaryContent.isVisible(), true);
    assert.equal(await secondaryContent.isHidden(), true);
    assert.equal(await primaryToggle.locator("svg").getAttribute("class"), "lucide lucide-eye-off");
    assert.deepEqual(
      await primaryContent.evaluate(element => {
        const style = getComputedStyle(element);
        return { textOverflow: style.textOverflow, whiteSpace: style.whiteSpace };
      }),
      { textOverflow: "ellipsis", whiteSpace: "nowrap" },
    );

    const desktopActionMetrics = await primarySecretRow.locator(".action-menu > summary").evaluate(element => {
      const style = getComputedStyle(element);
      return { width: element.getBoundingClientRect().width, height: element.getBoundingClientRect().height, borderStyle: style.borderStyle };
    });
    assert.deepEqual(desktopActionMetrics, { width: 34, height: 34, borderStyle: "solid" });
    const primaryActionMenu = primarySecretRow.locator(".action-menu");
    await primaryActionMenu.locator("summary").focus();
    await page.keyboard.press("Enter");
    assert.notEqual(await primaryActionMenu.getAttribute("open"), null);
    await page.keyboard.press("Escape");
    assert.equal(await primaryActionMenu.getAttribute("open"), null);
    await page.waitForFunction(() => !document.querySelector("[data-copy-password][data-state]"));
    const secondaryActionMenu = secondarySecretRow.locator(".action-menu");
    await secondaryActionMenu.locator("summary").click();
    const tableMenuMetrics = await secondaryActionMenu.evaluate(menu => {
      const panel = menu.querySelector(":scope > div");
      const shell = menu.closest(".table-shell");
      const panelBounds = panel.getBoundingClientRect();
      const shellBounds = shell.getBoundingClientRect();
      const triggerBounds = menu.querySelector(":scope > summary").getBoundingClientRect();
      const probe = document.elementFromPoint(panelBounds.left + 12, panelBounds.bottom - 12);
      return {
        position: getComputedStyle(panel).position,
        opensAbove: panelBounds.bottom < triggerBounds.top,
        insideShellWidth: panelBounds.left >= shellBounds.left && panelBounds.right <= shellBounds.right,
        visible: probe === panel || panel.contains(probe),
      };
    });
    assert.deepEqual(tableMenuMetrics, {
      position: "absolute",
      opensAbove: true,
      insideShellWidth: true,
      visible: true,
    });
    await assertNoTableHorizontalScrollbar(page, "variables desktop action menu");
    await saveSnapshot(page, "variables-menu-open");
    await page.keyboard.press("Escape");
    assert.equal(await secondaryActionMenu.getAttribute("open"), null);
    await primaryToggle.focus();
    assert.match(await primarySecretRow.locator("[data-copy-password]").getAttribute("aria-label"), /^Copy variable value/);
    await saveSnapshot(page, "variables");

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await assertNoHorizontalOverflow(page, "variables mobile");
    const mobileControlSizes = await primarySecretRow.locator("[data-toggle-password], [data-copy-text], .action-menu > summary").evaluateAll(elements =>
      elements.map(element => {
        const bounds = element.getBoundingClientRect();
        return { width: bounds.width, height: bounds.height };
      }),
    );
    assert.ok(mobileControlSizes.every(size => size.width >= 44 && size.height >= 44), JSON.stringify(mobileControlSizes));
    await page.evaluate(() => {
      document.activeElement?.blur();
      window.scrollTo(0, 0);
    });
    const mobileActionMenu = secondarySecretRow.locator(".action-menu");
    await mobileActionMenu.locator("summary").click();
    const mobileMenuMetrics = await mobileActionMenu.evaluate(menu => {
      const panel = menu.querySelector(":scope > div");
      const panelBounds = panel.getBoundingClientRect();
      const probe = document.elementFromPoint(panelBounds.left + 12, panelBounds.bottom - 12);
      return {
        insideViewport: panelBounds.left >= 0 && panelBounds.right <= window.innerWidth,
        visible: probe === panel || panel.contains(probe),
      };
    });
    assert.deepEqual(mobileMenuMetrics, { insideViewport: true, visible: true });
    await assertNoHorizontalOverflow(page, "variables mobile action menu");
    await page.evaluate(() => {
      document.activeElement?.blur();
      window.scrollTo(0, 0);
    });
    await page.waitForTimeout(50);
    await saveSnapshot(page, "variables-menu-mobile-open");
    await page.keyboard.press("Escape");
    await page.evaluate(() => {
      document.activeElement?.blur();
      window.scrollTo(0, 0);
    });
    await page.waitForTimeout(50);
    assert.equal(await page.locator(".skip-link").evaluate(element => element.matches(":focus")), false);
    await saveSnapshot(page, "variables-mobile");
    await page.setViewportSize({ width: 1440, height: 1000 });

    const chineseContext = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      locale: "zh-CN",
    });
    const chinesePage = await chineseContext.newPage();
    await chinesePage.goto(`${fixture.baseURL}/login`);
    assert.equal(await chinesePage.getAttribute("html", "lang"), "zh-CN");
    assert.equal(await chinesePage.locator("h1").textContent(), "登录");
    await Promise.all([
      chinesePage.waitForNavigation(),
      chinesePage.locator('form.login-locale button[name="locale"][value="en-US"]').click(),
    ]);
    assert.equal(await chinesePage.getAttribute("html", "lang"), "en-US");
    assert.equal(await chinesePage.locator("h1").textContent(), "Sign in");
    await Promise.all([
      chinesePage.waitForNavigation(),
      chinesePage.locator('form.login-locale button[name="locale"][value="zh-CN"]').click(),
    ]);
    assert.equal(await chinesePage.getAttribute("html", "lang"), "zh-CN");
    await chinesePage.locator('input[name="username"]').fill("admin");
    await chinesePage.locator('input[name="password"]').fill("calibration-ledger-2026");
    await Promise.all([
      chinesePage.waitForURL("**/monitor"),
      chinesePage.locator('[data-login-form] button[type="submit"]').click(),
    ]);
    await Promise.all([
      chinesePage.waitForNavigation(),
      chinesePage.locator('form[action="/settings/locale"] button[name="locale"][value="en-US"]').click(),
    ]);
    assert.equal(await chinesePage.getAttribute("html", "lang"), "en-US");
    assert.equal(await chinesePage.locator("main h1").textContent(), "Host overview");
    await chineseContext.close();

    const noScriptContext = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      locale: "en-US",
      javaScriptEnabled: false,
    });
    const noScriptPage = await noScriptContext.newPage();
    await noScriptPage.goto(`${fixture.baseURL}/login`);
    await noScriptPage.locator('input[name="username"]').fill("admin");
    await noScriptPage.locator('input[name="password"]').fill("calibration-ledger-2026");
    await Promise.all([
      noScriptPage.waitForURL("**/monitor"),
      noScriptPage.locator('button[type="submit"]').first().click(),
    ]);
    await noScriptPage.goto(`${fixture.baseURL}/resources/variables`);
    const noScriptRow = noScriptPage.locator("tbody tr").filter({ hasText: "PRIMARY_TOKEN" });
    assert.equal(await noScriptRow.locator("[data-password-value]").isHidden(), true);
    const noScriptSecret = noScriptRow.locator(".no-js-secret");
    assert.equal(await noScriptSecret.getAttribute("open"), null);
    await noScriptSecret.locator("summary").click();
    assert.notEqual(await noScriptSecret.getAttribute("open"), null);
    assert.match(await noScriptSecret.locator("code").textContent(), /line-one\r?\nline-two/);
    const noScriptActionMenu = noScriptPage.locator("tbody tr").last().locator(".action-menu");
    await noScriptActionMenu.locator("summary").click();
    assert.equal(await noScriptActionMenu.evaluate(menu => {
      const panelBounds = menu.querySelector(":scope > div").getBoundingClientRect();
      const triggerBounds = menu.querySelector(":scope > summary").getBoundingClientRect();
      const probe = document.elementFromPoint(panelBounds.left + 12, panelBounds.bottom - 12);
      return panelBounds.bottom < triggerBounds.top && (probe === menu || menu.contains(probe));
    }), true);
    await noScriptPage.goto(`${fixture.baseURL}/resources/files/`);
    assert.equal(await noScriptPage.locator("[data-file-drop-form]").count(), 1);
    assert.equal(await noScriptPage.locator('[data-file-drop-form] input[type="file"][multiple]').count(), 1);
    assert.equal((await noScriptPage.locator("[data-file-drop-form] button[type='submit']").textContent()).trim(), "Start upload");
    await noScriptPage.goto(`${fixture.baseURL}/resources/files/view/documentation/recovery-checklist.md`);
    assert.equal(await noScriptPage.locator("[data-markdown-preview]").isHidden(), true);
    assert.equal(await noScriptPage.locator("[data-markdown-source]").isVisible(), true);
    assert.match(await noScriptPage.locator("[data-markdown-source]").textContent(), /# Recovery checklist/);
    await noScriptPage.goto(`${fixture.baseURL}/resources/files/view/automation/weekly-system-check.ps1`);
    assert.equal(await noScriptPage.locator("[data-script-preview]").isVisible(), true);
    assert.equal(await noScriptPage.locator("[data-script-preview]").getAttribute("class"), null);
    assert.match(await noScriptPage.locator("[data-script-preview]").textContent(), /param\(\[string\]\$Environment/);
    await noScriptPage.goto(`${fixture.baseURL}/config/schedules`);
    assert.equal(await noScriptPage.locator('[data-group-name="Operations"] [data-group-body]').isVisible(), true);
    await noScriptPage.goto(`${fixture.baseURL}/config/quick-runs`);
    assert.equal(await noScriptPage.locator('[data-group-name="Operations"] [data-group-body]').isVisible(), true);
    await noScriptContext.close();

    assert.deepEqual(consoleErrors, [], `Browser console errors:\n${consoleErrors.join("\n")}`);
    process.stdout.write(`Chromium desktop gate passed. Snapshots: ${snapshotRoot}\n`);
  } finally {
    if (browser) await browser.close();
    fixture.child.kill("SIGINT");
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
