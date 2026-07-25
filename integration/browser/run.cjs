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

async function saveSnapshot(page, name) {
  await page.screenshot({
    path: path.join(snapshotRoot, `${name}.png`),
    fullPage: true,
    animations: "disabled",
  });
}

async function createVariable(page, name, value, password = false) {
  await page.locator('a[href="/resources/variables/new"]').first().click();
  await page.waitForURL("**/resources/variables/new");
  await page.locator('[data-task-panel] input[name="name"]').fill(name);
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

    await page.goto(`${fixture.baseURL}/resources/files/automation`);
    await page.waitForFunction(() => document.querySelector('a[href="/resources/files/run/automation/weekly-system-check.ps1"] svg'));
    await page.locator('a[href="/resources/files/run/automation/weekly-system-check.ps1"]').click();
    await page.waitForURL("**/resources/files/run/automation/weekly-system-check.ps1");
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

    await page.goto(`${fixture.baseURL}/monitor`);
    await page.locator("[data-host-overview]").waitFor();
    await page.waitForTimeout(250);
    await saveSnapshot(page, "monitor");

    await page.goto(`${fixture.baseURL}/resources/files/`);
    await page.locator('a[href^="/resources/files/new-directory"]').click();
    await page.waitForURL("**/resources/files/new-directory?path=");
    await page.locator("[data-task-panel]").waitFor();
    await page.keyboard.press("Escape");
    await page.waitForURL("**/resources/files/");
    await page.locator("[data-task-panel]").waitFor({ state: "detached" });
    await saveSnapshot(page, "files");

    await page.goto(`${fixture.baseURL}/resources/variables`);
    await createVariable(page, "DEPLOY_REGION", "west-europe");
    await createVariable(page, "PRIMARY_TOKEN", "line-one\nline-two-with-a-long-value-that-must-not-expand-the-table", true);
    await createVariable(page, "SECONDARY_TOKEN", "second-secret", true);

    const primarySecretRow = page.locator("tbody tr").filter({ hasText: "PRIMARY_TOKEN" });
    const secondarySecretRow = page.locator("tbody tr").filter({ hasText: "SECONDARY_TOKEN" });
    const primaryToggle = primarySecretRow.locator("[data-toggle-password]");
    const primaryContent = primarySecretRow.locator("[data-password-content]");
    const secondaryContent = secondarySecretRow.locator("[data-password-content]");

    assert.equal(await primarySecretRow.locator("[data-password-mask]").textContent(), "••••••••");
    assert.equal(await primaryContent.isHidden(), true);
    assert.equal(await secondaryContent.isHidden(), true);
    assert.deepEqual(
      await primarySecretRow.locator(".secret-controls button").evaluateAll(buttons => buttons.map(button => button.hasAttribute("data-toggle-password") ? "toggle" : "copy")),
      ["toggle", "copy"],
    );

    await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: fixture.baseURL });
    await primarySecretRow.locator("[data-copy-password]").click();
    await primarySecretRow.locator('[data-copy-password][data-state="success"]').waitFor();
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
    assert.match(await secondarySecretRow.locator("[data-password-status]").textContent(), /Copy failed/);
    assert.equal(await secondaryContent.isHidden(), true);

    await primaryToggle.focus();
    await page.keyboard.press("Enter");
    assert.equal(await primaryToggle.getAttribute("aria-expanded"), "true");
    assert.match(await primaryToggle.getAttribute("aria-label"), /^Hide variable value/);
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
    await primaryToggle.focus();
    await page.locator("[data-copy-password][data-state]").first().waitFor({ state: "detached" });
    assert.match(await primarySecretRow.locator("[data-copy-password]").getAttribute("aria-label"), /^Copy variable value/);
    await saveSnapshot(page, "variables");

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await assertNoHorizontalOverflow(page, "variables mobile");
    const mobileControlSizes = await primarySecretRow.locator("[data-toggle-password], [data-copy-password], .action-menu > summary").evaluateAll(elements =>
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
