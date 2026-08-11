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

const build = spawnSync("go", ["build", "-buildvcs=false", "-o", fixtureBinary, "./integration/browser/fixture"], {
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
      const match = output.match(/READY (http:\/\/127\.0\.0\.1:\d+) HOST_ROOT ([A-Za-z0-9_-]+)/);
      if (match) {
        clearTimeout(timeout);
        resolve({ child, baseURL: match[1], hostRoot: Buffer.from(match[2], "base64url").toString("utf8") });
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

function hostFileURL(baseURL, endpoint, hostPath, parameters = {}) {
  const target = new URL(endpoint, baseURL);
  target.searchParams.set("path", hostPath);
  for (const [name, value] of Object.entries(parameters)) {
    if (value !== undefined && value !== null && value !== "") target.searchParams.set(name, String(value));
  }
  return target.toString();
}

function hostFileHref(endpoint, hostPath, parameters = {}) {
  const target = new URL(hostFileURL("http://scriptboard.invalid", endpoint, hostPath, parameters));
  return `${target.pathname}${target.search}`;
}

async function assertFocusReturns(page, locator, message) {
  // Dialog teardown restores focus asynchronously. Loaded Windows CI runners
  // can observe the close click before that focus task runs, so wait for the
  // user-visible state instead of sampling document.activeElement immediately.
  const target = await locator.elementHandle();
  assert.ok(target, `${message}: focus target is missing`);
  try {
    await page.waitForFunction(element => document.activeElement === element, target, { timeout: 3000 });
  } catch {
    assert.equal(await locator.evaluate(element => element === document.activeElement), true, message);
  } finally {
    await target.dispose();
  }
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

async function assertDeferredMainNavigation(page) {
  let releaseResponse;
  const responseGate = new Promise(resolve => {
    releaseResponse = resolve;
  });
  let resolveRequestStarted;
  const requestStarted = new Promise(resolve => {
    resolveRequestStarted = resolve;
  });
  const routeHandler = async route => {
    const request = route.request();
    if (request.headers()["x-scriptboard-navigation"] !== "pjax") {
      await route.continue().catch(error => {
        if (!String(error).includes("already handled")) throw error;
      });
      return;
    }
    if (request.headers()["x-scriptboard-data"] === "shell") {
      await route.continue();
      return;
    }
    resolveRequestStarted();
    await responseGate;
    await route.continue().catch(error => {
      if (!String(error).includes("already handled")) throw error;
    });
  };
  await page.route("**/resources/variables", routeHandler);
  try {
    await page.locator('.sidebar-nav a[href="/resources/variables"]').click();
    assert.equal(new URL(page.url()).pathname, "/resources/variables", "main navigation did not update the URL immediately");
    assert.equal(await page.title(), "Variables · ScriptBoard", "main navigation did not update the title immediately");
    assert.equal(
      await page.locator('.sidebar-nav a[href="/resources/variables"]').getAttribute("aria-current"),
      "page",
      "main navigation did not update the active tab immediately",
    );
    await page.getByRole("heading", { name: "Variables", exact: true }).waitFor({ timeout: 500 });
    await page.getByText("Password type only hides the value by default in this page.", { exact: false }).waitFor();
    const loading = page.locator('[data-deferred-region] [data-deferred-state="loading"]');
    await loading.waitFor({ state: "visible", timeout: 500 });
    assert.match((await loading.textContent()).trim(), /^Loading/);
    assert.equal(
      await page.locator('main[data-navigation-state="loading"]').count(),
      0,
      "heavy-data navigation replaced the whole main region",
    );
    await requestStarted;
    releaseResponse();
    await loading.waitFor({ state: "detached" });
    assert.equal(
      await page.locator("[data-deferred-region] [data-deferred-state]").count(),
      0,
      "loaded data region kept a loading or error state",
    );
  } finally {
    releaseResponse();
    await page.unroute("**/resources/variables", routeHandler);
  }

  assert.equal(
    await page.evaluate(() => window.__scriptboardStatusIntervalCount),
    1,
    "PJAX navigation created another common-status poller",
  );
  await page.locator('.sidebar-nav a[href="/monitor"]').click();
  await page.locator("main h1").getByText("Host overview", { exact: true }).waitFor();
}

async function assertRapidMainNavigationIgnoresLateResponses(page) {
  let releaseVariables;
  const variablesGate = new Promise(resolve => {
    releaseVariables = resolve;
  });
  let resolveVariablesStarted;
  const variablesStarted = new Promise(resolve => {
    resolveVariablesStarted = resolve;
  });
  const routeHandler = async route => {
    if (route.request().headers()["x-scriptboard-navigation"] !== "pjax") {
      await route.continue();
      return;
    }
    resolveVariablesStarted();
    await variablesGate;
    await route.continue().catch(error => {
      if (!String(error).includes("already handled")) throw error;
    });
  };
  await page.route("**/resources/variables", routeHandler);
  await page.evaluate(() => {
    window.__scriptboardNativeFetch = window.fetch;
    window.fetch = (input, options = {}) => {
      const requestURL = new URL(typeof input === "string" ? input : input.url, location.href);
      if (requestURL.pathname === "/resources/variables") {
        const { signal: _signal, ...optionsWithoutSignal } = options;
        return window.__scriptboardNativeFetch(input, optionsWithoutSignal);
      }
      return window.__scriptboardNativeFetch(input, options);
    };
  });
  try {
    await page.locator('.sidebar-nav a[href="/resources/variables"]').click();
    await variablesStarted;
    await page.locator('.sidebar-nav a[href="/config/quick-runs"]').click();
    assert.equal(new URL(page.url()).pathname, "/config/quick-runs");
    assert.equal(await page.title(), "Quick Runs · ScriptBoard");
    await page.getByRole("heading", { name: "Quick Runs", exact: true }).waitFor();
    releaseVariables();
    await page.waitForTimeout(100);
    assert.equal(new URL(page.url()).pathname, "/config/quick-runs", "late response changed the URL");
    assert.equal(
      await page.getByRole("heading", { name: "Quick Runs", exact: true }).count(),
      1,
      "late response replaced the current page",
    );
  } finally {
    releaseVariables();
    await page.unroute("**/resources/variables", routeHandler);
    await page.evaluate(() => {
      if (window.__scriptboardNativeFetch) {
        window.fetch = window.__scriptboardNativeFetch;
        delete window.__scriptboardNativeFetch;
      }
    });
  }
}

async function assertNavigationFailureCanRetry(page) {
  await page.evaluate(() => {
    window.__scriptboardNativeFetch = window.fetch;
    let failNextRequest = true;
    window.fetch = (input, options) => {
      const requestURL = new URL(typeof input === "string" ? input : input.url, location.href);
      const requestHeaders = new Headers(options?.headers || {});
      const isShellRequest = requestHeaders.get("X-ScriptBoard-Data") === "shell";
      if (failNextRequest && requestURL.pathname === "/resources/variables" && !isShellRequest) {
        failNextRequest = false;
        return Promise.reject(new TypeError("Simulated navigation failure"));
      }
      return window.__scriptboardNativeFetch(input, options);
    };
  });
  try {
    await page.locator('.sidebar-nav a[href="/resources/variables"]').click();
    const errorState = page.locator('[data-deferred-region] [data-deferred-state="error"]');
    await errorState.waitFor();
    assert.equal(new URL(page.url()).pathname, "/resources/variables");
    await page.getByRole("heading", { name: "Variables", exact: true }).waitFor();
    assert.equal((await errorState.getByRole("heading").textContent()).trim(), "Unable to load this page");
    const retry = errorState.getByRole("button", { name: "Retry", exact: true });
    assert.equal(await retry.locator("svg.lucide-rotate-ccw").count(), 1, "retry action does not use its Lucide icon");
    await retry.click();
    await page.getByRole("heading", { name: "Variables", exact: true }).waitFor();
    await errorState.waitFor({ state: "detached" });
  } finally {
    await page.evaluate(() => {
      window.fetch = window.__scriptboardNativeFetch;
      delete window.__scriptboardNativeFetch;
    });
  }
}

async function assertServerErrorNavigationPreservesWorkspace(page) {
  await page.goto(new URL("/monitor", page.url()).toString());
  const workspaceURL = page.url();
  const accountLink = page.locator('.app-sidebar a[href="/settings/account"]');
  const routeHandler = async route => {
    if (route.request().headers()["x-scriptboard-navigation"] !== "pjax") {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 500,
      contentType: "text/html; charset=utf-8",
      body: `<!doctype html><html lang="en-US"><head><title>Operation not completed · ScriptBoard</title></head><body>
        <main class="workspace error-page">
          <p class="error-code">HTTP 500</p>
          <h1>Operation not completed</h1>
          <div class="page-error" role="alert">ScriptBoard could not complete this operation.</div>
          <details class="ledger-disclosure"><summary><span>Technical details</span></summary><div class="disclosure-body"><code>Unable to read account settings</code></div></details>
        </main>
      </body></html>`,
    });
  };
  await page.route("**/settings/account", routeHandler);
  try {
    await accountLink.click();
    const dialog = page.getByRole("dialog", { name: "Operation not completed" });
    await dialog.waitFor();
    assert.equal(page.url(), workspaceURL, "server error navigation changed the workspace URL");
    assert.equal(await page.getByRole("heading", { name: "Host overview", exact: true }).count(), 1);
    assert.match(await dialog.textContent(), /HTTP\s*500/);
    assert.match(await dialog.textContent(), /ScriptBoard could not complete this operation/);
    await dialog.getByText("Technical details", { exact: true }).click();
    assert.match(await dialog.textContent(), /Unable to read account settings/);
    await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
    await dialog.waitFor({ state: "detached" });
    await assertFocusReturns(page, accountLink, "closing the error dialog did not restore focus");
  } finally {
    await page.unroute("**/settings/account", routeHandler);
  }
}

async function assertServerErrorTaskPanelPreservesWorkspace(page) {
  await page.goto(new URL("/resources/variables", page.url()).toString());
  const workspaceURL = page.url();
  const taskLink = page.locator('a[href="/resources/variables/new"][data-task-link]').first();
  const routeHandler = route => route.fulfill({
    status: 500,
    contentType: "text/html; charset=utf-8",
    body: `<!doctype html><html lang="en-US"><body><main class="workspace error-page">
      <p class="error-code">HTTP 500</p><h1>Operation not completed</h1>
      <div class="page-error" role="alert">ScriptBoard could not complete this operation.</div>
      <details class="ledger-disclosure"><summary>Technical details</summary><div class="disclosure-body"><code>Unable to prepare the variable task</code></div></details>
    </main></body></html>`,
  });
  await page.route("**/resources/variables/new", routeHandler);
  try {
    await taskLink.click();
    const dialog = page.getByRole("dialog", { name: "Operation not completed" });
    await dialog.waitFor();
    assert.equal(page.url(), workspaceURL, "task server error changed the workspace URL");
    assert.equal(await page.getByRole("heading", { name: "Variables", exact: true }).count(), 1);
    assert.equal(await page.locator("[data-task-panel]").count(), 0, "failed task left an empty task panel behind");
    assert.equal(await dialog.getByRole("button", { name: "Reopen", exact: true }).count(), 1, "task failure did not offer a safe GET retry");
    await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
    await assertFocusReturns(page, taskLink, "task error did not restore focus");
  } finally {
    await page.unroute("**/resources/variables/new", routeHandler);
  }
}

async function assertNativePostServerErrorPreservesWorkspace(page) {
  await page.goto(new URL("/monitor/applications", page.url()).toString());
  const workspaceURL = page.url();
  const pinButton = page.getByRole("button", { name: "Pin api-prod", exact: true });
  const routeHandler = route => route.fulfill({
    status: 500,
    contentType: "text/html; charset=utf-8",
    body: `<!doctype html><html lang="en-US"><body><main class="workspace error-page">
      <p class="error-code">HTTP 500</p><h1>Operation not completed</h1>
      <div class="page-error" role="alert">ScriptBoard could not complete this operation.</div>
      <details class="ledger-disclosure"><summary>Technical details</summary><div class="disclosure-body"><code>Unable to pin application</code></div></details>
    </main></body></html>`,
  });
  await page.route("**/monitor/applications/*/pin", routeHandler);
  try {
    await pinButton.click();
    const dialog = page.getByRole("dialog", { name: "Operation not completed" });
    await dialog.waitFor();
    assert.equal(page.url(), workspaceURL, "native POST server error replaced the workspace URL");
    assert.equal(await page.locator("[data-applications-page]").count(), 1, "native POST server error replaced the applications page");
    assert.match(await dialog.textContent(), /Not retried automatically/);
    assert.equal(await dialog.getByRole("button", { name: "Submit again", exact: true }).count(), 0, "write failure offered an unsafe automatic resubmission");
    assert.equal(await dialog.getByRole("button", { name: "Refresh current page", exact: true }).count(), 1, "write failure did not offer a safe state refresh");
    await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
    await assertFocusReturns(page, pinButton, "native POST error did not restore focus");
  } finally {
    await page.unroute("**/monitor/applications/*/pin", routeHandler);
  }
}

async function assertAsyncPostServerErrorPreservesWorkspace(page) {
  await page.goto(new URL("/settings/updates", page.url()).toString());
  const workspaceURL = page.url();
  const restartButton = page.getByRole("button", { name: "Restart service", exact: true });
  assert.equal(await restartButton.count(), 1, "managed-service fixture did not expose the restart control");
  await saveSnapshot(page, "updates-service-control");
  const desktopViewport = page.viewportSize();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(workspaceURL);
  assert.equal(await page.getByRole("button", { name: "Restart service", exact: true }).count(), 1, "mobile updates page hid the restart control");
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "restart control caused mobile horizontal overflow");
  await saveSnapshot(page, "updates-service-control-mobile");
  await page.setViewportSize(desktopViewport);
  await page.goto(workspaceURL);
  await page.locator("[data-update-source-open]").click();
  await saveSnapshot(page, "update-source-drawer");
  const checkForm = page.locator('form[action="/settings/updates/check"][data-async]');
  const checkButton = checkForm.getByRole("button");
  const routeHandler = route => route.fulfill({
    status: 500,
    contentType: "text/html; charset=utf-8",
    body: `<!doctype html><html lang="en-US"><body><main class="workspace error-page">
      <p class="error-code">HTTP 500</p><h1>Operation not completed</h1>
      <div class="page-error" role="alert">ScriptBoard could not complete this operation.</div>
      <details class="ledger-disclosure"><summary>Technical details</summary><div class="disclosure-body"><code>Unable to check for updates</code></div></details>
    </main></body></html>`,
  });
  await page.route("**/settings/updates/check", routeHandler);
  try {
    await checkButton.click();
    const dialog = page.getByRole("dialog", { name: "Operation not completed" });
    await dialog.waitFor();
    assert.equal(page.url(), workspaceURL, "async POST server error changed the workspace URL");
    assert.equal(await page.locator("[data-updates-page]").count(), 1, "async POST server error replaced the updates page");
    assert.match(await dialog.textContent(), /Unable to check for updates/);
    await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
    await assertFocusReturns(page, checkButton, "async POST error did not restore focus");
    await page.locator("[data-update-source-close]").last().click();
  } finally {
    await page.unroute("**/settings/updates/check", routeHandler);
  }
}

async function assertHistoryNavigationUsesLoadingState(page) {
  await page.locator('.sidebar-nav a[href="/config/quick-runs"]').click();
  await page.getByRole("heading", { name: "Quick Runs", exact: true }).waitFor();

  let releaseBack;
  const backGate = new Promise(resolve => {
    releaseBack = resolve;
  });
  const variablesRoute = async route => {
    if (route.request().headers()["x-scriptboard-data"] === "shell") {
      await route.continue();
      return;
    }
    await backGate;
    await route.continue().catch(error => {
      if (!String(error).includes("already handled")) throw error;
    });
  };
  await page.route("**/resources/variables", variablesRoute);
  await page.evaluate(() => history.back());
  await page.waitForURL("**/resources/variables");
  await page.getByRole("heading", { name: "Variables", exact: true }).waitFor({ timeout: 500 });
  const backLoading = page.locator('[data-deferred-region] [data-deferred-state="loading"]');
  await backLoading.waitFor({ timeout: 500 });
  releaseBack();
  await backLoading.waitFor({ state: "detached" });
  await page.unroute("**/resources/variables", variablesRoute);

  let releaseForward;
  const forwardGate = new Promise(resolve => {
    releaseForward = resolve;
  });
  const quickRunsRoute = async route => {
    if (route.request().headers()["x-scriptboard-data"] === "shell") {
      await route.continue();
      return;
    }
    await forwardGate;
    await route.continue().catch(error => {
      if (!String(error).includes("already handled")) throw error;
    });
  };
  await page.route("**/config/quick-runs", quickRunsRoute);
  await page.evaluate(() => history.forward());
  await page.waitForURL("**/config/quick-runs");
  await page.getByRole("heading", { name: "Quick Runs", exact: true }).waitFor({ timeout: 500 });
  const forwardLoading = page.locator('[data-deferred-region] [data-deferred-state="loading"]');
  await forwardLoading.waitFor({ timeout: 500 });
  releaseForward();
  await forwardLoading.waitFor({ state: "detached" });
  await page.unroute("**/config/quick-runs", quickRunsRoute);
}

async function assertExpiredSessionUsesFullNavigation(page, context) {
  await context.clearCookies();
  await page.locator('.sidebar-nav a[href="/resources/variables"]').click();
  await page.waitForURL("**/login");
  await page.getByRole("heading", { name: "Sign in", exact: true }).waitFor();
  assert.equal(await page.locator("[data-app-shell]").count(), 0, "expired session kept the authenticated shell");

  await page.locator('input[name="username"]').fill("admin");
  await page.locator('input[name="password"]').fill("calibration-ledger-2026");
  await Promise.all([
    page.waitForURL("**/monitor"),
    page.locator('[data-login-form] button[type="submit"]').click(),
  ]);
  await page.locator("[data-app-shell]").waitFor();
}

async function assertApplicationMonitoring(page, baseURL) {
  await page.goto(`${baseURL}/monitor/applications`);
  const applications = page.locator("[data-applications-page]");
  await applications.waitFor();
  assert.equal(
    await page.locator('.sidebar-nav a[href="/monitor/applications"]').getAttribute("aria-current"),
    "page",
    "Applications navigation is not selected",
  );
  await page.locator('[data-running-applications-list] [data-kind="docker"]').filter({ hasText: "api-prod" }).waitFor();
  await page.getByText("Host Agent", { exact: true }).waitFor();
  assert.equal(await page.locator('.applications-sort-fields select[name="sort"]').inputValue(), "cpu");
  assert.equal(await page.locator('th[aria-sort="descending"] [data-application-sort="cpu"]').count(), 1);
  assert.match((await page.locator("[data-running-applications-list] [data-application-row]").first().textContent()).trim(), /api-prod/);
  const memorySort = page.locator('[data-application-sort="memory"]');
  await memorySort.click();
  await page.waitForFunction(() => new URL(location.href).searchParams.get("sort") === "memory" &&
    new URL(location.href).searchParams.get("direction") === "desc");
  assert.equal(await page.locator('th[aria-sort="descending"] [data-application-sort="memory"]').count(), 1);
  await memorySort.click();
  await page.waitForFunction(() => new URL(location.href).searchParams.get("sort") === "memory" &&
    new URL(location.href).searchParams.get("direction") === "asc");
  assert.equal(await page.locator('th[aria-sort="ascending"] [data-application-sort="memory"]').count(), 1);
  assert.equal(await page.locator('[data-running-applications-list] [data-kind="docker"] .application-kind').count(), 2);
  assert.equal(
    await page.locator('[data-running-applications-list] [data-kind="host"] .application-kind').count(),
    0,
    "host applications must not show a type tag",
  );
  assert.equal(await page.getByRole("button", { name: "View details", exact: true }).count(), 0);

  const pinnedRefresh = page.locator('[data-applications-refresh="pinned"]');
  const runningRefresh = page.locator('[data-applications-refresh="running"]');
  assert.equal(await pinnedRefresh.getAttribute("aria-checked"), "true");
  assert.equal(await runningRefresh.getAttribute("aria-checked"), "false");
  await runningRefresh.click();
  assert.equal(await runningRefresh.getAttribute("aria-checked"), "true");
  await runningRefresh.click();
  assert.equal(await runningRefresh.getAttribute("aria-checked"), "false");

  const apiRow = page.locator('[data-running-applications-list] [data-kind="docker"]').filter({ hasText: "api-prod" });
  await apiRow.click();
  const applicationDrawer = page.locator("[data-application-drawer]");
  await page.waitForFunction(() => document.querySelector(".application-drawer")?.getBoundingClientRect().left < innerWidth);
  await applicationDrawer.locator("[data-application-runtime-output] .application-runtime-facts").waitFor();
  assert.equal(await applicationDrawer.getAttribute("aria-hidden"), "false");
  assert.equal(await applicationDrawer.locator("[data-application-drawer-navigation]").isHidden(), true);
  assert.equal(await applicationDrawer.locator('[data-application-detail-panel="history"]').isHidden(), true);
  assert.equal(await applicationDrawer.locator('[data-application-detail-panel="runtime"]').isVisible(), true);
  assert.equal(await applicationDrawer.getByText("Only part of the runtime facts can be read", { exact: true }).count(), 0);
  await applicationDrawer.getByRole("button", { name: "Close application details", exact: true }).last().click();

  await Promise.all([
    page.waitForNavigation(),
    apiRow.getByRole("button", { name: "Pin api-prod", exact: true }).click(),
  ]);
  const pinnedAPI = page.locator("[data-pinned-applications] .pinned-application").filter({ hasText: "api-prod" });
  await pinnedAPI.waitFor();
  assert.match(await pinnedAPI.textContent(), /CPU/);
  assert.match(await pinnedAPI.textContent(), /Memory/);
  assert.match(await pinnedAPI.textContent(), /Disk I\/O/);
  await pinnedAPI.click();
  await page.waitForFunction(() => document.querySelector(".application-drawer")?.getBoundingClientRect().left < innerWidth);
  await page.waitForFunction(() => {
    const output = document.querySelector("[data-application-history-output]");
    return output?.children.length > 0 && !output.querySelector(".application-detail-loading");
  });
  await applicationDrawer.locator("[data-application-drawer-navigation]").waitFor({ state: "visible" });
  assert.equal(await applicationDrawer.locator("[data-application-drawer-navigation]").isVisible(), true);
  assert.equal(await applicationDrawer.locator('[data-application-detail-panel="history"]').isVisible(), true);
  assert.match(await applicationDrawer.textContent(), /Details do not refresh automatically/);
  const historyPath = applicationDrawer.locator("[data-application-history-output] svg path").first();
  if (await historyPath.count()) {
    assert.notEqual(
      await historyPath.evaluate(path => getComputedStyle(path).stroke),
      "none",
      "application history paths must have a visible stroke",
    );
  }
  await applicationDrawer.getByRole("tab", { name: /Runtime details/ }).click();
  assert.equal(await applicationDrawer.locator('[data-application-detail-panel="runtime"]').isVisible(), true);
  await applicationDrawer.getByRole("button", { name: "Close application details", exact: true }).last().click();
  await assertNoHorizontalOverflow(page, "Applications desktop");
  await saveSnapshot(page, "applications");

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await pinnedAPI.waitFor();
  await page.evaluate(() => document.activeElement?.blur());
  await page.waitForTimeout(50);
  assert.equal(
    await page.evaluate(() => document.activeElement?.classList.contains("skip-link") || false),
    false,
    "mobile snapshot retained focus on the skip link",
  );
  const mobileSkipLink = await page.locator(".skip-link").evaluate(element => {
    const bounds = element.getBoundingClientRect();
    return {
      focused: element.matches(":focus"),
      transform: getComputedStyle(element).transform,
      top: Math.round(bounds.top),
      bottom: Math.round(bounds.bottom),
    };
  });
  assert.ok(mobileSkipLink.bottom <= 0, `inactive mobile skip link remains visible: ${JSON.stringify(mobileSkipLink)}`);
  assert.equal(await page.locator(".applications-sort-fields").isVisible(), true);
  const mobilePinSizes = await page.locator(".pinned-application .icon-button").evaluateAll(elements =>
    elements.map(element => {
      const bounds = element.getBoundingClientRect();
      return { width: Math.round(bounds.width), height: Math.round(bounds.height) };
    }),
  );
  assert.ok(mobilePinSizes.every(size => size.width >= 44 && size.height >= 44), JSON.stringify(mobilePinSizes));
  const mobileRuntimeRow = page.locator('[data-running-applications-list] [data-kind="docker"]').filter({ hasText: "cache-local" });
  await mobileRuntimeRow.click();
  await page.waitForFunction(() => document.querySelector(".application-drawer")?.getBoundingClientRect().left < innerWidth);
  await applicationDrawer.locator("[data-application-runtime-output] .application-runtime-facts").waitFor();
  assert.equal(await applicationDrawer.locator("[data-application-drawer-navigation]").isHidden(), true);
  assert.equal(Math.round(await applicationDrawer.locator(".application-drawer").evaluate(element => element.getBoundingClientRect().width)), 390);
  await applicationDrawer.getByRole("button", { name: "Close application details", exact: true }).last().click();
  await assertNoHorizontalOverflow(page, "Applications mobile");
  await page.locator(".skip-link").evaluate(element => {
    element.style.display = "none";
  });
  await saveSnapshot(page, "applications-mobile");
  await page.setViewportSize({ width: 1440, height: 1000 });
}

async function assertLiveLogViewer(page, fixture) {
  const { baseURL } = fixture;
  await page.goto(hostFileURL(fixture.baseURL, "/resources/files/log", path.join(fixture.hostRoot, "data", "exports", "service.log")));
  const viewer = page.locator("[data-live-log-viewer]");
  const output = viewer.locator("[data-log-output]");
  await viewer.waitFor();
  await page.waitForFunction(() => document.querySelectorAll(".live-log-entry").length === 500);
  assert.equal(await viewer.locator('.live-log-entry[data-severity="error"]').count(), 1);
  assert.equal(await viewer.locator('.live-log-entry[data-severity="warning"]').count(), 0);
  const errorPresentation = await viewer.locator('.live-log-entry[data-severity="error"]').evaluate(element => ({
    level: element.querySelector(".live-log-entry__level")?.textContent,
    background: getComputedStyle(element).backgroundImage,
    rail: getComputedStyle(element).boxShadow,
  }));
  assert.equal(errorPresentation.level, "ERROR");
  assert.notEqual(errorPresentation.background, "none");
  assert.match(errorPresentation.rail, /rgb/);

  await output.evaluate(element => {
    element.scrollTop = 0;
  });
  await page.waitForFunction(() => document.querySelectorAll(".live-log-entry").length === 650);
  assert.equal(await viewer.locator('.live-log-entry[data-severity="warning"]').count(), 1);
  assert.equal(await viewer.locator("[data-log-autofollow]").getAttribute("aria-pressed"), "false");

  await viewer.locator("[data-log-copy]").click();
  await page.waitForFunction(() => document.querySelector("[data-log-copy-label]")?.textContent.trim() === "Copied");
  assert.match(await page.evaluate(() => navigator.clipboard.readText()), /line 650 request ERROR fixture boundary/);

  const pause = viewer.locator("[data-log-pause]");
  await pause.click();
  assert.equal((await viewer.locator("[data-log-state-label]").textContent()).trim(), "Paused");
  assert.equal((await viewer.locator("[data-log-pause-label]").textContent()).trim(), "Resume");
  await pause.click();
  await page.waitForFunction(() => document.querySelector("[data-log-state]")?.dataset.state === "live");

  await assertNoHorizontalOverflow(page, "File live logs desktop");
  await saveSnapshot(page, "live-logs-file");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await page.waitForFunction(() => document.querySelectorAll(".live-log-entry").length === 500);
  await assertNoHorizontalOverflow(page, "File live logs mobile");
  await saveSnapshot(page, "live-logs-file-mobile");

  await viewer.locator("[data-log-clear]").click();
  assert.equal(await viewer.locator(".live-log-entry").count(), 0);
  await page.setViewportSize({ width: 1440, height: 1000 });

  await page.goto(`${baseURL}/monitor/applications`);
  const dockerLogURL = await page.locator(
    '[data-running-applications-list] [data-kind="docker"] .application-log-link',
  ).first().getAttribute("href");
  assert.ok(dockerLogURL, "Docker rows do not expose a log entry");
  await page.goto(`${baseURL}${dockerLogURL}`);
  await page.waitForFunction(() => document.querySelectorAll(".live-log-entry").length >= 4);
  assert.equal(await page.locator('.live-log-entry[data-severity="error"]').count(), 1);
  assert.ok(await page.locator('.live-log-entry[data-severity="warning"]').count() >= 2);
  await assertNoHorizontalOverflow(page, "Docker live logs desktop");
  await saveSnapshot(page, "live-logs-docker");
}

async function assertWebsiteMonitoring(page, baseURL) {
  const monitorName = "Production API";
  const targetURL = `${baseURL}/missing-scriptboard-monitor-target`;
  await page.goto(`${baseURL}/monitor/websites/new`);
  const form = page.locator("[data-website-monitor-form]");
  const successMode = form.locator('select[name="http_success_mode"]');
  assert.deepEqual(await successMode.locator("option").allTextContents(), ["200–399", "Status codes or ranges", "Any HTTP response"]);
  await successMode.selectOption("exact");
  await form.locator('input[name="expected_statuses"]').fill("200;401-403;503");
  await form.locator('input[name="name"]').fill(monitorName);
  await form.locator('select[name="kind"]').selectOption("http");
  await form.locator('input[name="url"]').fill(targetURL);
  await form.locator('select[name="frequency_seconds"]').selectOption("30");
  await form.locator('select[name="timeout_seconds"]').selectOption("3");
  await Promise.all([
    page.waitForURL(url => /^\/monitor\/websites\/[^/]+$/.test(url.pathname) && !url.pathname.endsWith("/new")),
    form.locator('button[type="submit"]').click(),
  ]);
  const detailURL = page.url();
  await page.waitForFunction(async url => {
    const response = await fetch(`${url}/data`, { cache: "no-store" });
    if (!response.ok) return false;
    const snapshot = await response.json();
    return Number(snapshot.CheckedToken || snapshot.checkedToken || 0) > 0;
  }, detailURL);

  await page.setViewportSize({ width: 1440, height: 600 });
  await page.goto(`${baseURL}/monitor/websites`);
  for (const taskPath of ["/monitor/websites/new", "/monitor/websites/nginx"]) {
    const taskLink = page.locator(`.website-heading-actions a[href="${taskPath}"][data-task-link]`);
    assert.equal(await taskLink.count(), 1);
    await taskLink.click();
    const websiteTaskPanel = page.locator('[data-task-panel] main[data-task-kind^="website-"]');
    await websiteTaskPanel.waitFor();
    assert.equal(await websiteTaskPanel.locator(':scope > .website-task-heading > a[href="/monitor/websites"]').count(), 0);
    await page.locator("[data-task-panel-close]").click();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
  }
  const refreshLink = page.locator('.website-heading-actions a').filter({ hasText: "Refresh" });
  assert.equal(await refreshLink.getAttribute("href"), "/monitor/websites");
  assert.equal(await refreshLink.getAttribute("data-native"), null);
  assert.equal(await refreshLink.getAttribute("data-website-refresh"), "");
  await page.evaluate(() => {
    const root = document.querySelector("[data-website-monitoring]");
    root.dataset.manualRefreshMarker = "preserved";
    root.dataset.countsToken = "stale";
  });
  const refreshResponse = page.waitForResponse(response =>
    new URL(response.url()).pathname === "/monitor/websites/data",
  );
  await refreshLink.click();
  assert.equal((await refreshResponse).status(), 200);
  await page.waitForFunction(() =>
    document.querySelector("[data-website-monitoring]")?.dataset.countsToken !== "stale",
  );
  assert.equal(
    await page.locator('[data-website-monitoring][data-manual-refresh-marker="preserved"]').count(),
    1,
  );
  const row = page.locator("[data-monitor-id]").filter({ hasText: monitorName });
  await row.waitFor();
  const attentionURL = page.locator(".website-alert").filter({ hasText: monitorName }).locator(".website-alert__url");
  await attentionURL.waitFor();
  assert.equal((await attentionURL.textContent()).trim(), targetURL);
  await page.evaluate(() => {
    const maximum = document.documentElement.scrollHeight - innerHeight;
    scrollTo(0, Math.min(320, maximum));
  });
  const rowExternal = row.locator(".website-row-external");
  assert.equal(await rowExternal.getAttribute("target"), "_blank");
  assert.equal(await rowExternal.getAttribute("rel"), "noopener noreferrer");
  const popupPromise = page.waitForEvent("popup");
  await rowExternal.click();
  const popup = await popupPromise;
  await popup.waitForLoadState("domcontentloaded");
  assert.equal(popup.url(), targetURL);
  await popup.close();

  await row.locator(".website-row-action").click();
  const panel = page.locator("[data-task-panel]");
  const detail = panel.locator("[data-website-detail]");
  await detail.waitFor();
  assert.equal(await detail.locator(".website-detail-back").count(), 0);
  assert.equal(Math.round(await panel.evaluate(element => element.getBoundingClientRect().width)), 760);
  assert.equal(await detail.locator(".website-open-external").getAttribute("target"), "_blank");
  const originalToken = await detail.getAttribute("data-monitor-checked");
  const scrollBefore = await detail.locator("[data-website-detail-scroll]").evaluate(element => {
    element.scrollTop = Math.min(360, element.scrollHeight - element.clientHeight);
    window.__websiteTaskHost = document.querySelector(".task-panel-host");
    window.__websiteTaskPanel = document.querySelector("[data-task-panel]");
    return element.scrollTop;
  });
  await detail.locator('[data-website-focus-key="check"]').click();
  await page.waitForFunction(token => {
    const current = document.querySelector("[data-website-detail]");
    return current && current.dataset.monitorChecked !== token;
  }, originalToken, { timeout: 20000 });
  const preserved = await page.evaluate(() => {
    const detailRoot = document.querySelector("[data-website-detail]");
    const scroller = detailRoot.querySelector("[data-website-detail-scroll]");
    return {
      sameHost: window.__websiteTaskHost === document.querySelector(".task-panel-host"),
      samePanel: window.__websiteTaskPanel === document.querySelector("[data-task-panel]"),
      hostCount: document.querySelectorAll(".task-panel-host").length,
      scrollTop: scroller.scrollTop,
      focusKey: document.activeElement?.dataset.websiteFocusKey || "",
      refreshed: detailRoot.querySelector("[data-website-refresh-status]")?.textContent.trim() || "",
    };
  });
  assert.equal(preserved.sameHost, true);
  assert.equal(preserved.samePanel, true);
  assert.equal(preserved.hostCount, 1);
  assert.ok(Math.abs(preserved.scrollTop - scrollBefore) <= 1, JSON.stringify({ scrollBefore, preserved }));
  assert.equal(preserved.focusKey, "check");
  assert.match(preserved.refreshed, /Latest result updated/);
  const securitySummary = detail.locator(".website-security-summary");
  const issuerValue = securitySummary.locator(".website-security-summary__issuer dd");
  await issuerValue.evaluate(element => {
    element.textContent = "CN=Cloudflare TLS Issuing ECC CA 3,O=SSL Corporation,C=US";
  });
  const securityLayout = await securitySummary.evaluate(element => {
    const result = element.querySelector(".website-security-summary__result").getBoundingClientRect();
    const issuer = element.querySelector(".website-security-summary__issuer");
    const issuerBounds = issuer.getBoundingClientRect();
    return {
      fits: element.scrollWidth <= element.clientWidth + 1,
      issuerFits: issuer.scrollWidth <= issuer.clientWidth + 1,
      resultAboveIssuer: result.bottom <= issuerBounds.top + 1,
      issuerWhiteSpace: getComputedStyle(issuer.querySelector("dd")).whiteSpace,
    };
  });
  assert.equal(securityLayout.fits, true, JSON.stringify(securityLayout));
  assert.equal(securityLayout.issuerFits, true, JSON.stringify(securityLayout));
  assert.equal(securityLayout.resultAboveIssuer, true, JSON.stringify(securityLayout));
  assert.equal(securityLayout.issuerWhiteSpace, "normal");
  await assertNoHorizontalOverflow(page, "Website detail drawer desktop");
  const listScrollBeforeClose = await page.evaluate(() => window.scrollY);
  assert.ok(listScrollBeforeClose > 0, `Website monitor list did not scroll before drawer close: ${listScrollBeforeClose}`);
  await page.keyboard.press("Escape");
  await panel.waitFor({ state: "detached" });
  const listScrollAfterClose = await page.evaluate(() => window.scrollY);
  assert.ok(
    Math.abs(listScrollAfterClose - listScrollBeforeClose) <= 1,
    JSON.stringify({ listScrollBeforeClose, listScrollAfterClose }),
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.reload();
  const refreshedRow = page.locator("[data-monitor-id]").filter({ hasText: monitorName });
  await refreshedRow.locator(".website-row-action").click();
  await panel.locator("[data-website-detail]").waitFor();
  assert.equal(await panel.locator('[data-website-detail-section="incident"].website-detail-incident--down').count(), 1);
  const checkSettings = panel.locator('[data-website-detail-section="settings"]');
  assert.equal(await checkSettings.locator(".website-settings-summary > div").count(), 4);
  assert.ok(await checkSettings.locator(".website-settings-list > div").count() >= 6);
  assert.equal((await checkSettings.locator(".website-settings-source").textContent()).trim(), "Added manually");
  await saveSnapshot(page, "website-monitor-detail");
  await page.keyboard.press("Escape");
  await panel.waitFor({ state: "detached" });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(detailURL);
  await page.locator("[data-website-detail]").waitFor();
  assert.equal(await page.locator(".website-open-external").getAttribute("target"), "_blank");
  await assertNoHorizontalOverflow(page, "Website detail mobile");
  await assertNoTableHorizontalScrollbar(page, "Website detail mobile");
  await saveSnapshot(page, "website-monitor-detail-mobile");
  await page.setViewportSize({ width: 1440, height: 1000 });
}

async function assertStatusDisplaySettings(page, baseURL) {
  await page.goto(`${baseURL}/settings/display`);
  const settings = page.locator("[data-display-settings]");
  await settings.waitFor();
  assert.equal(
    await page.locator('.settings-nav a[href="/settings/display"]').getAttribute("aria-current"),
    "page",
  );
  const magenta = settings.locator('input[name="website_fault_color"][value="magenta"]');
  await magenta.check();
  assert.equal(await page.locator("html").getAttribute("data-website-fault-color"), "magenta");
  assert.equal(await page.evaluate(() => localStorage.getItem("scriptboard.websiteFaultColor")), "magenta");
  const colors = await page.evaluate(() => {
    const style = getComputedStyle(document.documentElement);
    return {
      healthy: style.getPropertyValue("--success").trim(),
      fault: style.getPropertyValue("--website-fault").trim(),
    };
  });
  assert.notEqual(colors.healthy, colors.fault);
  await saveSnapshot(page, "settings-display");
  await page.goto(`${baseURL}/monitor/websites`);
  assert.equal(await page.locator("html").getAttribute("data-website-fault-color"), "magenta");
  await page.evaluate(() => {
    localStorage.setItem("scriptboard.websiteFaultColor", "red");
    document.documentElement.dataset.websiteFaultColor = "red";
  });
}

async function assertUserManagement(page, baseURL) {
  await page.goto(`${baseURL}/settings/users`);
  assert.equal((await page.locator("main h1").textContent()).trim(), "Users");
  const createLink = page.locator('a[href="/settings/users/create"][data-task-link]');
  assert.equal(await createLink.count(), 1);
  assert.equal(await page.locator('form[action="/settings/users"]').count(), 0);
  await createLink.click();
  const createPanel = page.locator('.task-panel [data-task-kind="user-create"]');
  await createPanel.waitFor();
  assert.deepEqual(
    await createPanel.locator('select[name="role"] option').evaluateAll(options =>
      options.map(option => ({ value: option.value, label: option.textContent.trim() }))),
    [
      { value: "maintainer", label: "Maintainer" },
      { value: "operator", label: "Operator" },
      { value: "viewer", label: "Viewer" },
    ],
  );
  const createForm = createPanel.locator('form[action="/settings/users"]');
  await createForm.locator('input[name="username"]').fill("browser-viewer");
  await createForm.locator('select[name="role"]').selectOption("viewer");
  await createForm.locator('button[type="submit"]').click();
  const generatedPassword = page.locator("[data-generated-password]");
  await generatedPassword.waitFor();
  const password = (await generatedPassword.textContent()).trim();
  assert.ok(password.length >= 20, "generated user password was not shown once");
  const viewerRow = page.locator('[data-username="browser-viewer"]');
  await viewerRow.waitFor();
  assert.equal(await viewerRow.locator("input, select").count(), 0);
  assert.equal((await viewerRow.locator(".user-role-label").textContent()).trim(), "Viewer");
  await viewerRow.locator(".user-account-link").click();
  const editPanel = page.locator('.task-panel [data-task-kind="user-edit"]');
  await editPanel.waitFor();
  assert.equal(await editPanel.locator('input[name="username"]').inputValue(), "browser-viewer");
  assert.equal(await editPanel.locator('select[name="role"]').inputValue(), "viewer");
  assert.equal(await editPanel.locator('form[action$="/reset-password"]').count(), 1);
  await page.locator("[data-task-panel-close]").click();
  await page.locator("[data-task-panel]").waitFor({ state: "detached" });
  await page.goto(`${baseURL}/settings/users`);
  assert.equal(await page.locator("[data-generated-password]").count(), 0);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await assertNoHorizontalOverflow(page, "Users mobile");
  assert.equal(await page.locator('[data-username="browser-viewer"]').count(), 1);
  await page.setViewportSize({ width: 1440, height: 1000 });
	return password;
}

async function assertMySQLManagement(page, baseURL) {
  await page.goto(`${baseURL}/resources/databases`);
  const workspace = page.locator("[data-mysql-workspace]");
  await workspace.waitFor();
  assert.equal((await workspace.locator("h1").textContent()).trim(), "Database backups");
  assert.equal(await workspace.locator('form[action="/resources/databases/instances"]').count(), 1);
  assert.equal(await workspace.locator('input[name="password"][type="password"]').count(), 1);
  assert.equal(await workspace.locator('form[action="/resources/databases/settings/tools"]').count(), 1);
  assert.match(await workspace.textContent(), /No database instances are configured/);
  await assertNoHorizontalOverflow(page, "MySQL database management");
  await saveSnapshot(page, "mysql-databases");
}

async function assertViewerCannotManageMySQL(browser, baseURL, password) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: "en-US" });
  const page = await context.newPage();
  await page.goto(`${baseURL}/login`);
  await page.locator('input[name="username"]').fill("browser-viewer");
  await page.locator('input[name="password"]').fill(password);
  await Promise.all([
    page.waitForURL("**/monitor"),
    page.locator('[data-login-form] button[type="submit"]').click(),
  ]);
  assert.equal(await page.locator('.app-sidebar a[href="/resources/databases"]').count(), 0);
  const response = await page.goto(`${baseURL}/resources/databases`);
  assert.equal(response.status(), 403);
  await context.close();
}

async function assertAccountSettings(page, baseURL) {
  await page.goto(`${baseURL}/settings/account`);
  assert.equal(await page.locator(".app-sidebar .sidebar-account").count(), 0);
  assert.equal(await page.locator('.app-sidebar a[href="/settings/users"]').count(), 0);
  assert.equal(await page.locator('.settings-nav a[href="/settings/users"]').count(), 1);
  assert.equal(await page.locator(".settings-nav a").count(), 5);
  assert.equal(await page.locator(".settings-layout").count(), 0);
  const settingsLayout = await page.locator(".settings-nav, .settings-content").evaluateAll(elements =>
    elements.map(element => ({
      className: element.className,
      top: element.getBoundingClientRect().top,
      bottom: element.getBoundingClientRect().bottom,
      display: getComputedStyle(element).display,
    })));
  assert.equal(settingsLayout[0].className, "settings-nav");
  assert.equal(settingsLayout[0].display, "flex");
  assert.equal(settingsLayout[1].className, "settings-content");
  assert.ok(settingsLayout[0].bottom < settingsLayout[1].top, "settings tabs are not above the content");
  assert.equal(await page.locator('.settings-content form[action="/logout"]').count(), 1);
  assert.equal(await page.locator('.settings-content input[name="current_password"]').count(), 0);
  assert.equal(await page.locator('.settings-content input[name="new_password"]').count(), 0);
  assert.equal((await page.locator(".settings-summary dd").textContent()).trim(), "admin");

  await page.locator('a[href="/settings/account/username"][data-task-link]').click();
  const usernamePanel = page.locator('.task-panel [data-task-kind="account-username"]');
  await usernamePanel.waitFor();
  assert.equal(await usernamePanel.locator('input[name="username"]').inputValue(), "admin");
  assert.equal(await usernamePanel.locator('input[name="current_password"]').count(), 1);
  await page.setViewportSize({ width: 390, height: 420 });
  const pinnedClose = await page.locator("[data-task-panel]").evaluate(panel => {
    const close = panel.querySelector("[data-task-panel-close]");
    const scroller = panel.querySelector("main[data-task-page]");
    const before = close.getBoundingClientRect();
    scroller.scrollTop = scroller.scrollHeight;
    const after = close.getBoundingClientRect();
    return { beforeTop: before.top, beforeRight: before.right, afterTop: after.top, afterRight: after.right, scrollTop: scroller.scrollTop };
  });
  assert.ok(pinnedClose.scrollTop > 0, JSON.stringify(pinnedClose));
  assert.equal(Math.round(pinnedClose.afterTop), Math.round(pinnedClose.beforeTop));
  assert.equal(Math.round(pinnedClose.afterRight), Math.round(pinnedClose.beforeRight));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator("[data-task-panel-close]").click();
  await page.locator("[data-task-panel]").waitFor({ state: "detached" });

  await page.locator('a[href="/settings/account/password"][data-task-link]').click();
  const passwordPanel = page.locator('.task-panel [data-task-kind="account-password"]');
  await passwordPanel.waitFor();
  assert.equal(await passwordPanel.locator('input[name="current_password"]').count(), 1);
  assert.equal(await passwordPanel.locator('input[name="new_password"]').count(), 1);
  assert.equal(await passwordPanel.locator('input[name="confirm_password"]').count(), 1);
  await page.locator("[data-task-panel-close]").click();
  await page.locator("[data-task-panel]").waitFor({ state: "detached" });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await assertNoHorizontalOverflow(page, "Account settings mobile");
  assert.equal(await page.locator('.settings-nav a[href="/settings/users"]').isVisible(), true);
  assert.equal(await page.locator(".settings-nav").evaluate(element => getComputedStyle(element).overflowX), "auto");
  await page.setViewportSize({ width: 1440, height: 1000 });
}

async function assertAssistantSettingsAndWorkspace(page, baseURL) {
  await page.goto(`${baseURL}/settings/ai`);
  await page.locator("[data-assistant-settings]").waitFor();
  await assertNoHorizontalOverflow(page, "AI settings");
  assert.equal(await page.locator(".settings-nav a").count(), 5);

  await page.locator("[data-add-llm]").click();
  const drawer = page.locator('[data-llm-drawer][data-open="true"]');
  await drawer.waitFor();
  await drawer.locator('input[name="name"]').fill("Fixture · DeepSeek");
  await drawer.locator('select[name="provider"]').selectOption("openai-compatible");
  await drawer.locator('input[name="model"]').fill("fixture-model");
  await drawer.locator('input[name="endpoint"]').fill("http://127.0.0.1:11434/v1");
  await drawer.locator('input[name="api_key"]').fill("browser-fixture-key");
  await drawer.locator('input[name="make_default"]').check();
  await saveSnapshot(page, "ai-settings-drawer");
  await Promise.all([
    page.waitForURL("**/settings/ai"),
    drawer.locator('button[type="submit"]').click(),
  ]);

  const configuredRow = page.locator('[data-llm-id][data-name="Fixture · DeepSeek"]');
  await configuredRow.waitFor();
  assert.equal(await configuredRow.locator('input:not([type="hidden"])').count(), 0);
  assert.match(await configuredRow.textContent(), /Credential configured/);

  const connectionForm = configuredRow.locator("form[data-connection-test]");
  const connectionRoute = route => route.fulfill({
    status: 200,
    contentType: "application/json; charset=utf-8",
    body: JSON.stringify({ ok: false, message: "Upstream refused connection" }),
  });
  await page.route("**/settings/ai/llms/*/test", connectionRoute);
  try {
    await connectionForm.locator('button[type="submit"]').click();
    const failureDialog = page.locator(".connection-test-dialog");
    await failureDialog.waitFor();
    assert.match(await failureDialog.textContent(), /Upstream refused connection/);
    const inlineResult = connectionForm.locator("[data-connection-test-result]");
    assert.equal(await inlineResult.textContent(), "", "connection failure was repeated beside the test button");
    assert.equal(await inlineResult.evaluate(element => element.classList.contains("sr-only")), true, "empty connection failure status remained visible");
    await failureDialog.locator("[data-dialog-close]").last().click();
  } finally {
    await page.unroute("**/settings/ai/llms/*/test", connectionRoute);
  }
  if (process.env.SCRIPTBOARD_BROWSER_SCOPE === "connection-test") return;

  await configuredRow.locator("[data-edit-llm]").click();
  await drawer.waitFor();
  assert.equal(await drawer.locator('input[name="api_key"]').inputValue(), "");
  await drawer.locator("[data-close-llm]").last().click();
  await drawer.waitFor({ state: "hidden" });

  await page.locator("[data-open-guardrails]").click();
  await page.locator('[data-guardrail-drawer][data-open="true"]').waitFor();
  const policy = page.locator('form[action="/settings/ai/defaults"]');
  const enabledInput = policy.locator('input[name="enabled"]');
  if (!await enabledInput.isChecked()) await policy.locator('label:has(input[name="enabled"])').click();
  const defaultApprovalInput = policy.locator('input[name="default_auto_approval"]');
  if (await defaultApprovalInput.isChecked()) await policy.locator('label:has(input[name="default_auto_approval"])').click();
  await policy.locator('select[name="max_active_conversations"]').selectOption("2");
  await policy.locator('button[type="submit"]').click();
  await page.waitForTimeout(300);
  assert.equal(await page.locator('form[action="/settings/ai/defaults"] [data-async-submit-error]').count(), 0);
  assert.equal(await page.locator('form[action="/settings/ai/defaults"] input[name="enabled"]').isChecked(), true);

  const offlineRuntime = page.locator(".assistant-runtime-offline");
  const offlineForm = offlineRuntime.locator('form[action="/settings/ai/runtime/offline"]');
  assert.equal(await offlineForm.getAttribute("enctype"), "multipart/form-data");
  assert.equal(await offlineForm.getAttribute("data-native"), "");
  for (const field of ["runtime_manifest", "runtime_signature", "runtime_archive"]) {
    const input = offlineForm.locator(`input[type="file"][name="${field}"]`);
    assert.equal(await input.count(), 1);
    assert.equal(await input.getAttribute("required"), "");
  }
  await offlineRuntime.locator("summary").click();
  assert.equal(await offlineForm.isVisible(), true);
  await assertNoHorizontalOverflow(page, "AI settings offline Runtime expanded");
  await saveSnapshot(page, "ai-settings");

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await page.locator(".assistant-runtime-offline summary").click();
  await assertNoHorizontalOverflow(page, "AI settings mobile");
  await saveSnapshot(page, "ai-settings-mobile");
  await page.setViewportSize({ width: 1440, height: 1000 });

  await page.goto(`${baseURL}/ai`);
  await page.locator("[data-assistant-workspace]").waitFor();
  await assertNoHorizontalOverflow(page, "AI workspace");
  assert.equal((await page.locator("[data-model-picker-label]").textContent()).trim(), "Fixture · DeepSeek");

  const modelToggle = page.locator("[data-model-picker-toggle]");
  await modelToggle.click();
  const modelPicker = page.locator('.assistant-model-picker[data-open="true"]');
  await modelPicker.waitFor();
  assert.equal(await modelPicker.locator('[role="option"][aria-selected="true"]').count(), 1);
  await saveSnapshot(page, "ai-chat-model-picker");
  await modelToggle.click();

  const approvalToggle = page.locator("[data-auto-approval-toggle]");
  assert.equal(await approvalToggle.getAttribute("aria-pressed"), "false");
  await approvalToggle.click();
  assert.equal(await approvalToggle.getAttribute("aria-pressed"), "true");
  assert.equal(await page.locator('[role="dialog"]').count(), 0);

  await page.locator("[data-resource-picker-toggle]").click();
  const resourcePicker = page.locator('[data-resource-picker][data-open="true"]');
  await resourcePicker.waitFor();
  const hostResource = resourcePicker.locator('[data-resource-kind="directory"][data-resource-id="host"]');
  assert.equal(await hostResource.count(), 1);
  await hostResource.click();
  const directoryResource = resourcePicker.locator('[data-resource-kind="directory"][data-resource-label="automation"]');
  assert.equal(await directoryResource.count(), 1);
  await directoryResource.click();
  const fileResource = resourcePicker.locator('[data-resource-kind="file"][data-resource-label="README.md"]');
  assert.equal(await fileResource.count(), 1);
  await fileResource.click();
  assert.equal((await page.locator("[data-assistant-context-count]").textContent()).trim(), "3");
  await saveSnapshot(page, "ai-chat");

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await assertNoHorizontalOverflow(page, "AI workspace mobile");
  await saveSnapshot(page, "ai-chat-mobile");
  await page.setViewportSize({ width: 1440, height: 1000 });
}

async function assertExternalInterfaces(page, fixture) {
  await page.goto(`${fixture.baseURL}/config/external-interfaces`);
  await page.locator("[data-external-interfaces-page]").waitFor();
  assert.match(await page.locator("h1").textContent(), /External Interfaces/);
  await assertNoHorizontalOverflow(page, "External Interfaces empty state");

  await page.goto(`${fixture.baseURL}/config/external-interfaces/keys/new`);
  const keyForm = page.locator(".external-task-sheet form");
  await keyForm.locator('input[name="label"]').fill("Browser fixture");
  await keyForm.locator('select[name="duration"]').selectOption("1d");
  await keyForm.locator('button[type="submit"]').click();
  const maskedSecret = (await page.locator(".external-secret code").textContent()).trim();
  assert.match(maskedSecret, /^sbk_[A-Za-z0-9_-]{16}\.••••[A-Za-z0-9_-]{4}$/);
  const copyKey = page.locator("[data-copy-key]");
  await copyKey.click();
  await copyKey.locator('[data-copy-key-label]').getByText("Copied").waitFor();
  const secret = await page.evaluate(() => navigator.clipboard.readText());
  assert.match(secret, /^sbk_[A-Za-z0-9_-]{16}\.[A-Za-z0-9_-]{43}$/);
  const keyID = secret.slice(4).split(".")[0];

  await page.goto(`${fixture.baseURL}/config/external-interfaces/keys/${keyID}/entries/new`);
  const form = page.locator("[data-external-entry-form]");
  await form.locator('select[name="action_type"]').selectOption("upload");
  assert.equal(await form.locator('[data-external-action-fields="upload"]').isVisible(), true);
  assert.equal(await form.locator('[data-external-action-fields="log"]').isVisible(), false);
  await form.locator('input[name="label"]').fill("Artifact upload");
  await form.locator('input[name="name"]').fill("artifact");
  await form.locator('input[name="upload_directory"]').fill(path.join(fixture.hostRoot, "data", "exports"));
  await form.locator('input[name="upload_max_bytes"]').fill("1024");
  await form.locator('input[name="upload_extensions"]').fill(".txt");
  await form.locator('select[name="upload_conflict"]').selectOption("rename");
  await form.locator('button[type="submit"]').click();
  await page.locator("[data-external-interfaces-page]").waitFor();
  assert.equal(await page.getByText("Artifact upload", { exact: true }).count(), 1);

  const trigger = await page.request.post(`${fixture.baseURL}/trigger?name=artifact`, {
    headers: { Authorization: `Bearer ${secret}` },
    multipart: { file: { name: "external-result.txt", mimeType: "text/plain", buffer: Buffer.from("fixture complete") } },
  });
  assert.equal(trigger.status(), 201);
  assert.equal((await trigger.json()).action, "upload");

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await assertNoHorizontalOverflow(page, "External Interfaces mobile");
  await page.setViewportSize({ width: 1440, height: 1000 });
}

(async () => {
  const fixture = await startFixture();
  const fixtureHostPath = (...components) => path.join(fixture.hostRoot, ...components);
  const fixtureFilesURL = (relative = "", parameters = {}) =>
    hostFileURL(fixture.baseURL, "/resources/files", relative ? fixtureHostPath(...relative.split("/")) : fixture.hostRoot, parameters);
  const fixtureFileURL = (endpoint, relative, parameters = {}) =>
    hostFileURL(fixture.baseURL, endpoint, fixtureHostPath(...relative.split("/")), parameters);
  const fixtureFileHref = (endpoint, relative, parameters = {}) =>
    hostFileHref(endpoint, fixtureHostPath(...relative.split("/")), parameters);
  // GitHub Windows runners may serialize the same temporary path with either
  // a literal "~" or "%7E". Compare decoded query entries so a successful
  // navigation is not mistaken for a timeout.
  const matchesFixtureURL = expected => current => {
    const target = new URL(expected);
    return current.origin === target.origin && current.pathname === target.pathname &&
      JSON.stringify([...current.searchParams.entries()]) === JSON.stringify([...target.searchParams.entries()]);
  };
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
      permissions: ["clipboard-read", "clipboard-write"],
    });
    await context.addInitScript(() => {
      const originalSetInterval = window.setInterval.bind(window);
      window.__scriptboardStatusIntervalCount = 0;
      window.setInterval = (callback, delay, ...args) => {
        if (delay === 30000) window.__scriptboardStatusIntervalCount += 1;
        return originalSetInterval(callback, delay, ...args);
      };
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
    await assertDeferredMainNavigation(page);
    await assertRapidMainNavigationIgnoresLateResponses(page);
    await assertNavigationFailureCanRetry(page);
    await assertHistoryNavigationUsesLoadingState(page);
    await assertServerErrorNavigationPreservesWorkspace(page);
    await assertServerErrorTaskPanelPreservesWorkspace(page);
    await assertNativePostServerErrorPreservesWorkspace(page);
    await assertAsyncPostServerErrorPreservesWorkspace(page);
    await assertExpiredSessionUsesFullNavigation(page, context);
    if (process.env.SCRIPTBOARD_BROWSER_SCOPE === "navigation") {
      process.stdout.write("Chromium deferred-navigation regressions passed.\n");
      return;
    }
    if (process.env.SCRIPTBOARD_BROWSER_SCOPE === "connection-test") {
      await assertAssistantSettingsAndWorkspace(page, fixture.baseURL);
      process.stdout.write("Chromium connection-test error regressions passed.\n");
      return;
    }

    await assertApplicationMonitoring(page, fixture.baseURL);
    await assertLiveLogViewer(page, fixture);
    await assertWebsiteMonitoring(page, fixture.baseURL);
    await assertStatusDisplaySettings(page, fixture.baseURL);
    await assertAccountSettings(page, fixture.baseURL);
	const viewerPassword = await assertUserManagement(page, fixture.baseURL);
	await assertMySQLManagement(page, fixture.baseURL);
	await assertViewerCannotManageMySQL(browser, fixture.baseURL, viewerPassword);
    await assertAssistantSettingsAndWorkspace(page, fixture.baseURL);
    await assertExternalInterfaces(page, fixture);

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
    await page.goto(fixtureFileURL("/resources/files/view", "documentation/recovery-checklist.md"));
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
      fixtureFileHref("/resources/files/view", "README.md"),
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
      page.waitForURL(url => url.pathname === "/resources/files" && !url.searchParams.has("path")),
      page.locator('.app-sidebar a[href="/resources/files"]').click(),
    ]);
    await Promise.all([
      page.waitForURL(matchesFixtureURL(fixtureFilesURL())),
      page.getByRole("link", { name: path.basename(fixture.hostRoot), exact: true }).first().click(),
    ]);
    await Promise.all([
      page.waitForURL(matchesFixtureURL(fixtureFilesURL("automation"))),
      page.getByRole("link", { name: "automation", exact: true }).first().click(),
    ]);
    await Promise.all([
      page.waitForURL(matchesFixtureURL(fixtureFileURL("/resources/files/view", "automation/weekly-system-check.ps1"))),
      page.getByRole("link", { name: "weekly-system-check.ps1", exact: true }).first().click(),
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
      page.waitForURL(matchesFixtureURL(fixtureFilesURL("automation"))),
      page.locator(".task-back").click(),
    ]);
    const executableScriptRow = page.locator(".file-table tbody tr").filter({ hasText: "weekly-system-check.ps1" });
    await executableScriptRow.getByRole("link", { name: "Run", exact: true }).waitFor();
    const filesWorkspaceURL = page.url();
    await executableScriptRow.getByRole("link", { name: "Run", exact: true }).click();
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
    assert.equal(await page.locator(".task-panel-scrim").getAttribute("aria-label"), "Close");
    assert.equal(await page.locator("[data-task-panel] .button--primary").count(), 1);
    assert.equal(Math.round(await page.locator("[data-task-panel] .button--primary").evaluate(element => element.getBoundingClientRect().height)), 38);
    await page.locator('[data-task-panel] input[name="arguments"]').fill("-Environment staging");
    await saveSnapshot(page, "files-run-task");
    await Promise.all([
      page.waitForURL(/\/history\/runs\/[^/]+$/),
      page.locator('[data-task-panel] button[type="submit"]').click(),
    ]);
    await page.locator("[data-run-log]").waitFor();
    await page.waitForFunction(() => document.querySelector("[data-run-log]")?.textContent.includes("result=passed"));
    assert.match(await page.locator("[data-run-log]").textContent(), /environment=staging/);
    await page.waitForFunction(() => document.querySelector("[data-run-live-state]")?.textContent.includes("complete"));
    assert.equal((await page.locator("[data-run-status]").textContent()).trim(), "Succeeded");
    assert.equal(await page.locator("[data-run-stop-form]").count(), 0);
    await page.reload();
    await page.waitForFunction(() => document.querySelector("[data-run-live-state]")?.textContent.includes("complete"));
    const completedRunLog = await page.locator("[data-run-log]").textContent();
    const runLogRegression = {
      environmentOccurrences: completedRunLog.split("environment=staging").length - 1,
      resultOccurrences: completedRunLog.split("result=passed").length - 1,
      pauseControls: await page.locator("[data-run-pause]").count(),
    };
    assert.deepEqual(runLogRegression, {
      environmentOccurrences: 1,
      resultOccurrences: 1,
      pauseControls: 0,
    }, `completed Run log regression: ${JSON.stringify(runLogRegression)}`);
    await assertNoHorizontalOverflow(page, "run detail");
    await saveSnapshot(page, "run-detail");

    await page.goto(`${fixture.baseURL}/history/runs`);
    await page.locator(".runs-table").waitFor();
    await assertTableRowsAligned(page, ".runs-table", "runs desktop");
    assert.equal((await page.locator(".runs-table thead th").filter({ hasText: "Actor" }).textContent()).trim(), "Actor");
    const runActors = page.locator(".runs-table [data-run-initiator]");
    assert.ok(await runActors.count() >= 1);
    assert.equal((await runActors.first().textContent()).trim(), "admin");
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
      page.waitForURL(url => url.pathname === "/history/runs" &&
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
    const scheduleCreateGroupControl = page.locator('a[href="/config/schedules/groups/new"]');
    const scheduleCreateGroupContract = await scheduleCreateGroupControl.evaluate(element => ({
      classes: [...element.classList].sort(),
      insideHeadingActions: Boolean(element.closest(".heading-actions")),
    }));
    assert.deepEqual(scheduleCreateGroupContract, { classes: ["button"], insideHeadingActions: true });
    await scheduleCreateGroupControl.click();
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
    await scheduleTask.locator('input[name="script"]').fill(fixtureHostPath("automation", "weekly-system-check.ps1"));
    await scheduleTask.locator('input[name="arguments"]').fill("-Environment production");
    const rawCronEditor = scheduleTask.locator(".cron-raw-editor");
    assert.equal(await rawCronEditor.getAttribute("open"), null);
    await rawCronEditor.locator("summary").click();
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
    const scheduleMobileActions = await scheduleRecord.locator(".button--compact, .action-menu > summary").evaluateAll(elements =>
      elements.map(element => {
        const bounds = element.getBoundingClientRect();
        return { width: Math.round(bounds.width), height: Math.round(bounds.height) };
      }),
    );
    assert.ok(scheduleMobileActions.every(size => size.width >= 44 && size.height >= 44), JSON.stringify(scheduleMobileActions));
    const scheduleCreateGroupMobileSize = await page.locator('a[href="/config/schedules/groups/new"]').evaluate(element => {
      const bounds = element.getBoundingClientRect();
      return { width: Math.round(bounds.width), height: Math.round(bounds.height) };
    });
    await assertNoHorizontalOverflow(page, "grouped schedules mobile");
    await saveSnapshot(page, "schedules-mobile");
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.reload();
    await scheduleRecord.waitFor();
    const scheduleRunButton = scheduleRecord.getByRole("button", { name: "Run now", exact: true });
    assert.equal(await scheduleRunButton.evaluate(element => element.classList.contains("button--primary")), false);
    assert.equal(Math.round(await scheduleRunButton.evaluate(element => element.getBoundingClientRect().height)), 34);
    await saveSnapshot(page, "schedules");
    const scheduleID = await scheduleRecord.getAttribute("data-schedule-id");
    assert.ok(scheduleID);
    await scheduleRecord.getByRole("button", { name: "Run now", exact: true }).click();
    await page.waitForURL(/\/history\/runs\/[^/?]+$/);
    await page.locator('.status-chip[data-state="succeeded"]').waitFor({ timeout: 15_000 });
    await page.goto(`${fixture.baseURL}/config/schedules`);
    scheduleRecord = page.locator("[data-schedule-id]").filter({ hasText: "Nightly safety check" });
    await scheduleRecord.getByRole("link", { name: "View run history", exact: true }).click();
    await page.waitForURL(url => url.pathname === "/history/runs" &&
      url.searchParams.get("q") === "Nightly safety check" &&
      url.searchParams.get("schedule_id") === scheduleID &&
      url.searchParams.get("focus") === "search");
    assert.equal(await page.locator("#run-search").inputValue(), "Nightly safety check");
    assert.equal(await page.locator("#run-search").evaluate(input => document.activeElement === input), true);
    await page.getByText("Nightly safety check", { exact: true }).waitFor();

    await page.goto(fixtureFilesURL("automation", { q: "weekly", sort: "name", direction: "desc" }));
    const quickRunWorkspaceURL = page.url();
    const scriptRow = page.locator(".file-table tbody tr").filter({ hasText: "weekly-system-check.ps1" });
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
    const savedQuickRun = page.locator("[data-quick-run-id]").filter({ hasText: "Weekly safety check" });
    await savedQuickRun.waitFor();
    await savedQuickRun.getByText("No run history", { exact: true }).waitFor();
    assert.equal(await savedQuickRun.locator("[data-quick-run-history-entry]").count(), 0);
    assert.equal((await savedQuickRun.locator(".quick-run-history__duration strong").textContent()).trim(), "—");
    const quickHeadingActions = page.locator(".quick-run-heading-actions > .button");
    assert.equal(await quickHeadingActions.count(), 3);
    const quickHeadingMetrics = await quickHeadingActions.evaluateAll(actions => actions.map(action => {
      const bounds = action.getBoundingClientRect();
      return { top: Math.round(bounds.top), height: Math.round(bounds.height) };
    }));
    assert.equal(new Set(quickHeadingMetrics.map(metric => metric.top)).size, 1, JSON.stringify(quickHeadingMetrics));
    assert.equal(new Set(quickHeadingMetrics.map(metric => metric.height)).size, 1, JSON.stringify(quickHeadingMetrics));
    assert.deepEqual(
      await quickHeadingActions.evaluateAll(actions => actions.map(action => new URL(action.href).pathname)),
      ["/config/quick-runs/groups/new", "/config/quick-runs/one-time/new", "/config/quick-runs/from-source/new"],
    );

    const assertWorkingDirectoryTree = async (href, kind) => {
      await page.locator(`a[href="${href}"]`).click();
      const task = page.locator(`[data-task-panel] [data-task-kind="${kind}"]`);
      await task.waitFor();
      const workingDirectory = task.locator('input[name="working_directory"]');
      assert.equal(await workingDirectory.getAttribute("type"), "hidden");
      assert.equal(await task.locator('input[name="working_directory"]:not([type="hidden"])').count(), 0);
      const tree = task.locator('[role="tree"]');
      await tree.waitFor();
		const rootDirectory = tree.getByRole("treeitem", { name: "This host", exact: true });
      await rootDirectory.waitFor();
		assert.equal(await rootDirectory.getAttribute("aria-selected"), "false");
		const hostDirectory = tree.getByRole("treeitem", { name: path.basename(fixture.hostRoot), exact: true });
		await hostDirectory.waitFor();
		assert.equal(await hostDirectory.getAttribute("aria-selected"), "true");
		await hostDirectory.click();
      const automationDirectory = tree.getByRole("treeitem", { name: "automation", exact: true });
      await automationDirectory.waitFor();
      await automationDirectory.click();
		assert.equal(await workingDirectory.inputValue(), fixtureHostPath("automation"));
      assert.equal(await automationDirectory.getAttribute("aria-selected"), "true");
      await page.locator("[data-task-panel-close]").click();
      await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    };
    await assertWorkingDirectoryTree("/config/quick-runs/one-time/new", "one-time-run");
    await assertWorkingDirectoryTree("/config/quick-runs/from-source/new", "quick-create");

    const quickCreateGroupControl = page.locator('a[href="/config/quick-runs/groups/new"]');
    const quickCreateGroupContract = await quickCreateGroupControl.evaluate(element => ({
      classes: [...element.classList].sort(),
      insideHeadingActions: Boolean(element.closest(".heading-actions")),
    }));
    assert.deepEqual(quickCreateGroupContract, scheduleCreateGroupContract);
    await quickCreateGroupControl.click();
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
    assert.equal(await page.locator('[data-task-panel] .field-readonly code').textContent(), fixtureHostPath("automation", "weekly-system-check.ps1"));
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
    const quickRunButton = quickRunRow.getByRole("button", { name: "Run", exact: true });
    assert.equal(await quickRunButton.evaluate(element => element.classList.contains("button--primary")), false);
    assert.equal(Math.round(await quickRunButton.evaluate(element => element.getBoundingClientRect().height)), 34);
    await assertNoHorizontalOverflow(page, "Quick Runs desktop");
    await saveSnapshot(page, "quick-runs");

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    const quickRunMobileActions = await page.locator("[data-quick-run-id]").first().locator(".button--compact, .action-menu > summary").evaluateAll(elements =>
      elements.map(element => {
        const bounds = element.getBoundingClientRect();
        return { width: Math.round(bounds.width), height: Math.round(bounds.height) };
      }),
    );
    assert.ok(quickRunMobileActions.every(size => size.width >= 44 && size.height >= 44), JSON.stringify(quickRunMobileActions));
    const quickCreateGroupMobileSize = await page.locator('a[href="/config/quick-runs/groups/new"]').evaluate(element => {
      const bounds = element.getBoundingClientRect();
      return { width: Math.round(bounds.width), height: Math.round(bounds.height) };
    });
    assert.deepEqual(quickCreateGroupMobileSize, scheduleCreateGroupMobileSize);
    await page.evaluate(() => {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
      scrollTo(0, 0);
    });
    await assertNoHorizontalOverflow(page, "Quick Runs mobile");
    await saveSnapshot(page, "quick-runs-mobile");
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(`${fixture.baseURL}/monitor`);
    await page.locator("[data-host-overview]").waitFor();
    assert.equal(await page.locator('.overview-page > .page-heading a[href="/resources/files"]').count(), 0);
    await page.waitForTimeout(250);
    await saveSnapshot(page, "monitor");

    const hostFilesWorkspaceURL = fixtureFilesURL();
    await page.goto(hostFilesWorkspaceURL);
    const copyCurrentPath = page.getByRole("button", { name: "Copy current path" });
    await copyCurrentPath.waitFor({ state: "visible" });
    assert.equal((await copyCurrentPath.textContent()).trim(), "", "copy-current-path control should be icon-only");
    await copyCurrentPath.click();
    await page.waitForFunction(() => document.querySelector("[data-copy-current-path]")?.getAttribute("aria-label") === "Path copied");
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), fixture.hostRoot);
    await page.goto(fixtureFilesURL("automation"));
    const parentDirectory = page.getByRole("link", { name: "Back to parent folder" });
    assert.equal((await parentDirectory.textContent()).trim(), "", "parent-directory control should be icon-only");
    await Promise.all([
      page.waitForURL(hostFilesWorkspaceURL),
      parentDirectory.click(),
    ]);
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
    assert.equal(page.url(), hostFilesWorkspaceURL, "opening the Upload task changed the workspace URL");
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
    assert.equal(page.url(), hostFilesWorkspaceURL, "opening the New directory task changed the workspace URL");
    await page.goBack();
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    assert.equal(page.url(), hostFilesWorkspaceURL, "closing a task with Back changed the workspace URL");
    await page.goForward();
    await page.locator("[data-task-panel]").waitFor();
    assert.equal(page.url(), hostFilesWorkspaceURL, "restoring a task with Forward changed the workspace URL");
    await page.keyboard.press("Escape");
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    await page.evaluate(() => localStorage.removeItem("scriptboard.files.pinnedDirectories.v2"));
    await page.reload();

    const quickAccess = page.locator("[data-file-quick-access]");
    await quickAccess.waitFor({ state: "visible" });
    assert.equal(await quickAccess.getAttribute("open"), null, "Quick access was not collapsed on reload");
    assert.equal((await quickAccess.locator("[data-file-quick-count]").textContent()).trim(), "0");
    await quickAccess.locator("summary").click();
    const automationRow = page.locator(".file-table tbody tr").filter({
      has: page.getByRole("link", { name: "automation", exact: true }),
    });
    const automationPin = automationRow.getByRole("button", { name: "Pin directory" });
    await automationPin.waitFor({ state: "visible" });
    await automationPin.click();
    await page.waitForFunction(() => Array.from(document.querySelectorAll("[data-file-pin]"))
      .some(element => element.dataset.filePinLabel === "automation" && element.getAttribute("aria-pressed") === "true"));
    assert.equal(await automationPin.getAttribute("aria-pressed"), "true");
    assert.equal((await quickAccess.locator("[data-file-quick-count]").textContent()).trim(), "1");
    const automationQuickLink = quickAccess.getByRole("link", { name: /automation/ });
    await automationQuickLink.waitFor();
    await saveSnapshot(page, "files-quick-access");
    await quickAccess.locator("summary").click();
    await page.reload();
    const reloadedQuickAccess = page.locator("[data-file-quick-access]");
    await reloadedQuickAccess.waitFor({ state: "visible" });
    assert.equal(await reloadedQuickAccess.getAttribute("open"), null, "Quick access was not collapsed after reload");
    await reloadedQuickAccess.locator("summary").click();
    const reopenedQuickLink = reloadedQuickAccess.getByRole("link", { name: /automation/ });
    await reopenedQuickLink.evaluate(link => link.addEventListener("click", event => event.preventDefault(), { once: true }));
    await reopenedQuickLink.click();
    assert.equal(await reloadedQuickAccess.getAttribute("open"), null, "Quick access did not collapse after its shortcut was clicked");
    const filesTab = page.locator('.sidebar-nav a[href="/resources/files"]');
    await filesTab.click();
    await page.waitForFunction(() => document.querySelector("[data-file-quick-access]")?.hasAttribute("open"));
    const resetQuickAccess = page.locator("[data-file-quick-access]");
    await resetQuickAccess.waitFor({ state: "visible" });
    assert.notEqual(await resetQuickAccess.getAttribute("open"), null, "Quick access did not open after the Files tab was clicked");
    await Promise.all([
      page.waitForURL(matchesFixtureURL(fixtureFilesURL("automation"))),
      resetQuickAccess.getByRole("link", { name: /automation/ }).click(),
    ]);
    const shortcutDestinationQuickAccess = page.locator("[data-file-quick-access]");
    await shortcutDestinationQuickAccess.waitFor({ state: "visible" });
    assert.equal(await shortcutDestinationQuickAccess.getAttribute("open"), null, "Quick access was not collapsed after shortcut navigation");
    await page.goto(hostFilesWorkspaceURL);
    const restoredQuickAccess = page.locator("[data-file-quick-access]");
    await restoredQuickAccess.waitFor({ state: "visible" });
    assert.equal(await restoredQuickAccess.getAttribute("open"), null, "Quick access was not collapsed after direct navigation");
    assert.equal((await restoredQuickAccess.locator("[data-file-quick-count]").textContent()).trim(), "1");
    const restoredAutomationRow = page.locator(".file-table tbody tr").filter({
      has: page.getByRole("link", { name: "automation", exact: true }),
    });
    const restoredAutomationPin = restoredAutomationRow.getByRole("button", { name: "Unpin directory" });
    await restoredAutomationPin.click();
    await page.waitForFunction(() => document.querySelector("[data-file-quick-count]")?.textContent.trim() === "0");
    assert.equal((await restoredQuickAccess.locator("[data-file-quick-count]").textContent()).trim(), "0");

    const hiddenToggle = page.locator("[data-file-hidden-toggle]");
    assert.equal(await hiddenToggle.isChecked(), false);
    assert.equal(await page.getByText(".env", { exact: true }).count(), 0);
    await Promise.all([
      page.waitForURL(url => url.pathname === "/resources/files" && url.searchParams.get("path") === fixture.hostRoot && url.searchParams.get("show_hidden") === "1"),
      page.locator(".file-hidden-toggle").click(),
    ]);
    assert.equal(await page.locator("[data-file-hidden-toggle]").isChecked(), true);
    assert.equal(await page.locator("[data-file-hidden-toggle]").evaluate(input => document.activeElement === input), true);
    const hiddenFileName = page.getByText(".env", { exact: true }).first();
    await hiddenFileName.waitFor();
    assert.ok(await page.locator(".file-hidden-badge").count() >= 2);
    await saveSnapshot(page, "files-hidden");
    await Promise.all([
      page.waitForURL(url => url.pathname === "/resources/files" && url.searchParams.get("path") === fixture.hostRoot && !url.searchParams.has("show_hidden")),
      page.locator(".file-hidden-toggle").click(),
    ]);
    await hiddenFileName.waitFor({ state: "detached" });
    assert.equal(await page.getByText(".env", { exact: true }).count(), 0);

    const fileDropZone = page.locator("[data-file-drop-zone]");
    assert.equal(await fileDropZone.locator(".file-drop-feedback").isHidden(), true);
    assert.equal((await fileDropZone.locator("[data-file-drop-title]").textContent()).trim(), "");
    assert.equal(await page.locator('[data-file-drop-form] input[name="path"]').inputValue(), fixture.hostRoot);
    const dropData = await page.evaluateHandle(() => {
      const transfer = new DataTransfer();
      transfer.items.add(new File(["uploaded by drag and drop"], "drag-upload.txt", { type: "text/plain" }));
      return transfer;
    });
    await fileDropZone.dispatchEvent("dragenter", { dataTransfer: dropData });
    assert.equal(await fileDropZone.getAttribute("data-state"), "active");
    assert.equal(await fileDropZone.locator(".file-drop-feedback").isVisible(), true);
    assert.equal((await fileDropZone.locator("[data-file-drop-title]").textContent()).trim(), "Release to upload");
    await saveSnapshot(page, "files-drop-active");
    await fileDropZone.dispatchEvent("drop", { dataTransfer: dropData });
    const uploadResultsDialog = page.locator("dialog.upload-results-dialog[open]");
    await uploadResultsDialog.waitFor();
    assert.equal(page.url(), hostFilesWorkspaceURL, "enhanced upload left the current file directory");
    assert.equal((await uploadResultsDialog.getByRole("heading").textContent()).trim(), "Upload results");
    await uploadResultsDialog.getByText("drag-upload.txt", { exact: true }).waitFor();
    await saveSnapshot(page, "upload-results");
    await uploadResultsDialog.getByRole("link", { name: "Close", exact: true }).last().click();
    await uploadResultsDialog.waitFor({ state: "detached" });
    await page.getByRole("link", { name: "drag-upload.txt", exact: true }).waitFor();
    const duplicateDropData = await page.evaluateHandle(() => {
      const transfer = new DataTransfer();
      transfer.items.add(new File(["renamed duplicate upload"], "drag-upload.txt", { type: "text/plain" }));
      return transfer;
    });
    await fileDropZone.dispatchEvent("drop", { dataTransfer: duplicateDropData });
    const conflictDialog = page.locator("dialog.file-conflict-dialog[open]");
    await conflictDialog.waitFor();
    assert.equal((await conflictDialog.getByRole("heading").textContent()).trim(), "A file with this name already exists");
    assert.equal(await conflictDialog.getByRole("button", { name: "Skip", exact: true }).count(), 1);
    assert.equal(await conflictDialog.getByRole("button", { name: "Overwrite", exact: true }).count(), 1);
    assert.equal(await conflictDialog.getByRole("button", { name: "Rename", exact: true }).count(), 1);
    assert.equal(
      await conflictDialog.getByRole("button", { name: "Skip", exact: true }).evaluate(element => document.activeElement === element),
      true,
    );
    await saveSnapshot(page, "file-conflict");
    await conflictDialog.getByRole("button", { name: "Rename", exact: true }).click();
    const renamedUploadResultsDialog = page.locator("dialog.upload-results-dialog[open]");
    await renamedUploadResultsDialog.waitFor();
    await renamedUploadResultsDialog.getByText("drag-upload (2).txt", { exact: true }).waitFor();
    await renamedUploadResultsDialog.getByRole("link", { name: "Close", exact: true }).last().click();
    await renamedUploadResultsDialog.waitFor({ state: "detached" });
    await page.getByRole("link", { name: "drag-upload (2).txt", exact: true }).waitFor();
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
    const fileSearchButtonMetrics = await fileSearchButton.evaluate(element => {
      const style = getComputedStyle(element);
      const primary = element.closest(".file-search-primary");
      const input = primary.querySelector("input");
      return {
        height: Math.round(element.getBoundingClientRect().height),
        inputHeight: Math.round(input.getBoundingClientRect().height),
        minHeight: style.minHeight,
        padding: style.padding,
        alignSelf: style.alignSelf,
        parentHeight: Math.round(element.parentElement.getBoundingClientRect().height),
      };
    });
    assert.deepEqual(fileSearchButtonMetrics, {
      height: 42,
      inputHeight: 42,
      minHeight: "42px",
      padding: "8px 18px",
      alignSelf: "auto",
      parentHeight: 42,
    }, JSON.stringify(fileSearchButtonMetrics));
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
    await page.route(url => url.pathname === "/resources/files" &&
      url.searchParams.get("path") === fixture.hostRoot && url.searchParams.get("q") === "auto", async route => {
      await delayedSearchRequest;
      await route.continue();
    }, { times: 1 });
    await fileSearchInput.fill("auto");
    const searchButtonWidth = Math.round(await fileSearchButton.evaluate(button => button.getBoundingClientRect().width));
    await fileSearchInput.press("Enter");
    assert.equal(await fileSearch.getAttribute("aria-busy"), "true");
    assert.equal(await fileSearchButton.isDisabled(), true);
    assert.equal(await fileSearchButton.getAttribute("aria-busy"), "true");
    assert.equal(Math.round(await fileSearchButton.evaluate(button => button.getBoundingClientRect().width)), searchButtonWidth);
    assert.equal((await fileSearchButton.textContent()).trim(), "Searching…");
    const searchedFilesURL = page.waitForURL(url =>
      url.pathname === "/resources/files" && url.searchParams.get("path") === fixture.hostRoot && url.searchParams.get("q") === "auto");
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
        url.pathname === "/resources/files" &&
        url.searchParams.get("path") === fixture.hostRoot &&
        url.searchParams.get("q") === "auto" &&
        url.searchParams.get("sort") === "type" &&
        url.searchParams.get("direction") === "desc"),
      fileSort.locator("[data-sort-submit]").click(),
    ]);
    assert.equal(await page.locator("[data-search-input]").evaluate(input => document.activeElement === input), true);
    assert.match((await page.locator("[data-file-sort] summary").textContent()).replace(/\s+/g, " "), /Type · Descending/);

    await Promise.all([
      page.waitForURL(url =>
        url.pathname === "/resources/files" &&
        url.searchParams.get("path") === fixture.hostRoot &&
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

    await page.setViewportSize({ width: 820, height: 900 });
    await page.goto(hostFilesWorkspaceURL);
    await assertNoHorizontalOverflow(page, "files tablet");

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(hostFilesWorkspaceURL);
    await assertNoHorizontalOverflow(page, "files mobile");
    const fileDropMobileMetrics = await page.locator("[data-file-drop-zone]").evaluate(element => {
      const bounds = element.getBoundingClientRect();
      return {
        fitsWidth: bounds.right <= window.innerWidth,
        feedbackHidden: getComputedStyle(element.querySelector(".file-drop-feedback")).display === "none",
      };
    });
    assert.deepEqual(fileDropMobileMetrics, { fitsWidth: true, feedbackHidden: true });
    const fileHeadingActionSizes = await page.locator(".files-heading-actions .button").evaluateAll(elements =>
      elements.map(element => ({
        width: Math.round(element.getBoundingClientRect().width),
        height: Math.round(element.getBoundingClientRect().height),
      })),
    );
    assert.equal(fileHeadingActionSizes.length, 2);
    assert.equal(fileHeadingActionSizes[0].width, fileHeadingActionSizes[1].width);
    assert.ok(fileHeadingActionSizes.every(size => size.height >= 44));
    const fileLocationActionSizes = await page.locator(".file-location-actions .icon-button").evaluateAll(elements =>
      elements.map(element => ({
        width: Math.round(element.getBoundingClientRect().width),
        height: Math.round(element.getBoundingClientRect().height),
      })),
    );
    assert.equal(fileLocationActionSizes.length, 2);
    assert.ok(fileLocationActionSizes.every(size => size.width >= 44 && size.height >= 44));
    const mobileSearchMetrics = await page.locator(".file-search-primary").evaluate(primary => {
      const bounds = primary.getBoundingClientRect();
      const inputBounds = primary.querySelector("input").getBoundingClientRect();
      const buttonBounds = primary.querySelector("button").getBoundingClientRect();
      return {
        fitsWidth: bounds.right <= window.innerWidth,
        sameRow: inputBounds.top < buttonBounds.bottom && buttonBounds.top < inputBounds.bottom,
        buttonHeight: Math.round(buttonBounds.height),
        shortcutHidden: getComputedStyle(primary.querySelector("kbd")).display === "none",
      };
    });
    assert.deepEqual(mobileSearchMetrics, { fitsWidth: true, sameRow: true, buttonHeight: 44, shortcutHidden: true });
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

    await page.goto(hostFilesWorkspaceURL);
    const readmeRow = page.locator(".file-table tbody tr").filter({
      has: page.getByRole("link", { name: "README.md", exact: true }),
    });
    await readmeRow.locator(".action-menu summary").click();
    await readmeRow.getByRole("button", { name: "Move to trash" }).click();
    await readmeRow.waitFor({ state: "detached" });
    await page.goto(`${fixture.baseURL}/resources/trash`);
    await assertTableRowsAligned(page, ".records-table", "trash desktop");
    const trashRow = page.locator(".records-table tbody tr").filter({ hasText: "README.md" });
    await trashRow.waitFor();
    const restoreButton = trashRow.getByRole("button", { name: "Restore", exact: true });
    assert.equal(await restoreButton.evaluate(element => element.classList.contains("button--primary")), false);
    assert.equal(Math.round(await restoreButton.evaluate(element => element.getBoundingClientRect().height)), 34);
    assert.equal(await trashRow.getByRole("button", { name: "Purge permanently", exact: true }).evaluate(element => element.classList.contains("button--danger")), true);
    await saveSnapshot(page, "trash");

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
    await chinesePage.goto(`${fixture.baseURL}/settings/users`);
    assert.equal((await chinesePage.locator("main h1").textContent()).trim(), "用户");
    const createUserTask = chinesePage.locator('a[href="/settings/users/create"][data-task-link]');
    assert.equal(await createUserTask.count(), 1);
    await createUserTask.click();
    const createUserDrawer = chinesePage.locator(".task-panel--user-create");
    await createUserDrawer.waitFor({ state: "visible" });
    assert.equal((await createUserDrawer.locator('form[action="/settings/users"] button[type="submit"]').textContent()).trim(), "创建用户");
    await createUserDrawer.locator("[data-task-panel-close]").click();
    await chinesePage.setViewportSize({ width: 390, height: 844 });
    await chinesePage.reload();
    await assertNoHorizontalOverflow(chinesePage, "用户管理移动端");
    await chinesePage.setViewportSize({ width: 1440, height: 1000 });
	await chinesePage.goto(`${fixture.baseURL}/resources/databases`);
	assert.equal((await chinesePage.locator("main h1").textContent()).trim(), "数据库备份");
    await chinesePage.goto(`${fixture.baseURL}/monitor`);
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
    await noScriptPage.goto(`${fixture.baseURL}/resources/databases`);
    assert.equal(await noScriptPage.locator('[data-mysql-workspace] form[action="/resources/databases/instances"]').count(), 1);
    assert.equal(await noScriptPage.locator('[data-mysql-workspace] input[name="password"][type="password"]').count(), 1);
    await noScriptPage.goto(hostFilesWorkspaceURL);
    assert.equal(await noScriptPage.locator("[data-file-drop-form]").count(), 1);
    assert.equal(await noScriptPage.locator('[data-file-drop-form] input[type="file"][multiple]').count(), 1);
    assert.equal(await noScriptPage.locator("[data-file-drop-form]").isHidden(), true);
    assert.equal(await noScriptPage.locator(".file-drop-feedback").isVisible(), false);
    assert.equal(await noScriptPage.locator('a[href^="/resources/files/upload"][data-task-link]').count(), 1);
    await noScriptPage.goto(fixtureFileURL("/resources/files/view", "documentation/recovery-checklist.md"));
    assert.equal(await noScriptPage.locator("[data-markdown-preview]").isHidden(), true);
    assert.equal(await noScriptPage.locator("[data-markdown-source]").isVisible(), true);
    assert.match(await noScriptPage.locator("[data-markdown-source]").textContent(), /# Recovery checklist/);
    await noScriptPage.goto(fixtureFileURL("/resources/files/view", "automation/weekly-system-check.ps1"));
    assert.equal(await noScriptPage.locator("[data-script-preview]").isVisible(), true);
    assert.equal(await noScriptPage.locator("[data-script-preview]").getAttribute("class"), null);
    assert.match(await noScriptPage.locator("[data-script-preview]").textContent(), /param\(\[string\]\$Environment/);
    await noScriptPage.goto(`${fixture.baseURL}/config/schedules`);
    assert.equal(await noScriptPage.locator('[data-group-name="Operations"] [data-group-body]').isVisible(), true);
    await noScriptPage.goto(`${fixture.baseURL}/config/quick-runs`);
    assert.equal(await noScriptPage.locator('[data-group-name="Operations"] [data-group-body]').isVisible(), true);
    await noScriptPage.goto(`${fixture.baseURL}/settings/account`);
    assert.equal(await noScriptPage.locator('.settings-content input[name="current_password"]').count(), 0);
    await noScriptPage.locator('a[href="/settings/account/username"]').click();
    assert.equal(await noScriptPage.locator('[data-task-kind="account-username"]').count(), 1);
    await noScriptPage.goto(`${fixture.baseURL}/settings/users`);
    await noScriptPage.locator('[data-username="browser-viewer"] .user-account-link').click();
    assert.equal(await noScriptPage.locator('[data-task-kind="user-edit"]').count(), 1);
    assert.equal(await noScriptPage.locator('input[name="username"]').inputValue(), "browser-viewer");
    await noScriptPage.goto(`${fixture.baseURL}/monitor/applications?kind=docker&query=cache&sort=memory&direction=asc`);
    const noScriptRunning = noScriptPage.locator("[data-running-applications-list] [data-application-row]");
    assert.equal(await noScriptRunning.count(), 1);
    assert.match(await noScriptRunning.textContent(), /cache-local/);
    assert.equal(await noScriptPage.locator(".applications-sort-fields").count(), 1);
    assert.equal(await noScriptRunning.locator('form[method="post"] input[name="csrf_token"]').count(), 1);
    await noScriptContext.close();

    const expectedServerErrorConsole = "Failed to load resource: the server responded with a status of 500 (Internal Server Error)";
    const injectedServerErrors = consoleErrors.filter(message => message === expectedServerErrorConsole);
    const unexpectedConsoleErrors = consoleErrors.filter(message => message !== expectedServerErrorConsole);
    assert.equal(injectedServerErrors.length, 4, "the four injected 5XX responses were not all observed by Chromium");
    assert.deepEqual(unexpectedConsoleErrors, [], `Browser console errors:\n${unexpectedConsoleErrors.join("\n")}`);
    process.stdout.write(`Chromium desktop gate passed. Snapshots: ${snapshotRoot}\n`);
  } finally {
    if (browser) await browser.close();
    fixture.child.kill("SIGINT");
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
