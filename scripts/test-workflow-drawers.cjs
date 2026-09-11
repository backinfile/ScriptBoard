// Runs against a local test deployment with at least two retained process logs.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const assert = require("node:assert/strict"),
  fs = require("fs");
(async () => {
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    fs.mkdirSync(".scratch/workflow-deploy", { recursive: true });
    const page = await browser.newPage({
        viewport: { width: 1500, height: 950 },
      }),
      errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.on("dialog", (d) => d.accept());
    const base = process.env.WORKFLOW_TEST_URL || "http://127.0.0.1:18910";
    await page.goto(base + "/login");
    await page.fill(
      "[name=username]",
      process.env.WORKFLOW_TEST_USER || "admin",
    );
    await page.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await page.locator("button[type=submit]").first().click();
    await page.waitForURL((url) => url.pathname !== "/login");
    await page.goto(base + "/workflow");
    let navigations = 0;
    page.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === page.mainFrame()) navigations++;
    });
    await page.click("[data-wf-tab=editor]");
    await page.click(".wf-flow-menu summary");
    await page.click("[data-action=flow-new]");
    await page.click("[data-action=flow-fields]");
    await page.waitForTimeout(600);
    assert.equal(
      await page.locator("#wf-drawer-title").innerText(),
      "入口参数",
    );
    assert.equal(
      await page.locator('[data-schema="outputs:0:source"]').count(),
      0,
    );
    await page.screenshot({
      path: ".scratch/workflow-deploy/drawer-parameters.png",
    });
    await page.selectOption('[data-schema="outputs:0:type"]', "number");
    await page.fill('[data-schema="outputs:0:default"]', "invalid-number");
    await page.click("[data-action=drawer-save]");
    assert.equal(
      await page.locator("[data-wf-drawer]").evaluate((e) => e.open),
      true,
      "invalid default must not save a previous value",
    );
    await page.selectOption('[data-schema="outputs:0:type"]', "string");
    await page.fill('[data-schema="outputs:0:default"]', "dev");

    await page.click("[data-action=field-add]");
    await page.click("[data-action=drawer-save]");
    await page.locator("[data-wf-drawer] [role=alert]").waitFor();
    assert.match(
      await page.locator("[data-wf-drawer] [role=alert]").innerText(),
      /参数名称/,
    );
    await page.fill('[data-schema="outputs:1:name"]', "region");
    await page.click("[data-action=drawer-save]");
    await page.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await page.click("[data-action=flow-fields]");
    assert.equal(
      await page.inputValue('[data-schema="outputs:1:name"]'),
      "region",
    );
    await page.keyboard.press("Escape");
    await page.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await page.click("[data-action=flow-output-fields]");
    assert.equal(
      await page.locator("#wf-drawer-title").innerText(),
      "出口参数",
    );
    await page.click("[data-action=field-add][data-id=inputs]");
    await page.fill('[data-schema="inputs:0:name"]', "result");
    await page.click("[data-action=drawer-save]");
    await page.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await page.click("[data-action=flow-output-fields]");
    assert.equal(
      await page.inputValue('[data-schema="inputs:0:name"]'),
      "result",
    );
    await page.keyboard.press("Escape");
    await page.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await page.click("[data-action=flow-fields]");
    assert.equal(
      await page.inputValue('[data-schema="outputs:1:name"]'),
      "region",
    );
    await page.keyboard.press("Escape");
    await page.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await page.click("[data-wf-tab=custom]");
    await page.click("[data-action=custom-new]");
    await page.screenshot({
      path: ".scratch/workflow-deploy/drawer-custom.png",
    });
    await page.click("[data-action=drawer-close]");
    await page.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await page.click("[data-wf-tab=history]");
    await page.locator(".wf-run summary").first().click();
    const first = page.locator("[data-action=logs]").first(),
      idA = await first.getAttribute("data-id");
    await first.evaluate((e) => {
      for (let a = e.parentElement; a; a = a.parentElement)
        if (a.tagName === "DETAILS") a.open = true;
    });
    await first.click();
    await page.waitForSelector(".wf-log-line");
    await page.waitForTimeout(600);
    assert.equal(
      await page
        .locator("[data-wf-drawer] > section")
        .evaluate((e) => e.scrollWidth > e.clientWidth),
      false,
    );
    const original = await page.locator(".wf-log-lines").innerText();
    assert(original.length > 0);
    await page.screenshot({ path: ".scratch/workflow-deploy/drawer-logs.png" });
    await page.click("[data-action=drawer-close]");
    await page.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    const all = await page
      .locator("[data-action=logs]")
      .evaluateAll((es) => es.map((e) => e.dataset.id));
    const idB = all.find((id) => id !== idA);
    assert(idB, "need two process logs");
    let pendingResolve, release;
    const pending = new Promise((r) => (pendingResolve = r)),
      gate = new Promise((r) => (release = r));
    let countA = 0;
    const event = (text) => [
      { sequence: 1, time: new Date().toISOString(), source: "stdout", text },
    ];
    await page.route("**/workflow/logs/*", async (route) => {
      const id = route.request().url().split("/").pop();
      if (id === idA) {
        countA++;
        if (countA > 1) {
          pendingResolve();
          await gate;
        }
        await route.fulfill({ json: event("LOG_A") });
      } else await route.fulfill({ json: event("LOG_B") });
    });
    await first.click();
    await pending;
    await page.click("[data-action=drawer-close]");
    await page.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    const target = page
      .locator('[data-action=logs][data-id="' + idB + '"]')
      .first();
    await target.evaluate((e) => {
      for (let a = e.parentElement; a; a = a.parentElement)
        if (a.tagName === "DETAILS") a.open = true;
    });
    await target.click();
    await page.waitForFunction(() =>
      document.querySelector(".wf-log-lines").textContent.includes("LOG_B"),
    );
    release();
    await page.waitForTimeout(350);
    assert.match(await page.locator(".wf-log-lines").innerText(), /LOG_B/);
    assert.doesNotMatch(
      await page.locator(".wf-log-lines").innerText(),
      /LOG_A/,
    );
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(600);
    assert.equal(
      await page
        .locator("[data-wf-drawer] > section")
        .evaluate((e) => e.scrollWidth > e.clientWidth),
      false,
    );
    await page.screenshot({
      path: ".scratch/workflow-deploy/drawer-mobile.png",
    });
    assert.equal(navigations, 0);
    assert.deepEqual(errors, []);
    fs.writeFileSync(
      ".scratch/workflow-deploy/drawer-results.json",
      JSON.stringify(
        {
          passed: true,
          navigations,
          errors,
          checks: [
            "entry labels and persistence",
            "inline validation",
            "custom drawer",
            "real log long lines",
            "stale log response isolation",
            "mobile no overflow",
          ],
        },
        null,
        2,
      ),
    );
    console.log("DRAWERS AND LOGS PASS");
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
