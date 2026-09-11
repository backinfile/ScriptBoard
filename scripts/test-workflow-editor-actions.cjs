const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
(async () => {
  const b = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const p = await b.newPage({ viewport: { width: 1500, height: 1000 } }),
      errors = [];
    p.on("pageerror", (e) => errors.push(e.message));
    await p.goto("http://127.0.0.1:18910/login");
    await p.fill("[name=username]", "admin");
    await p.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await p.locator("button[type=submit]").first().click();
    await p.waitForTimeout(400);
    await p.goto("http://127.0.0.1:18910/workflow");
    let nav = 0;
    p.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === p.mainFrame()) nav++;
    });
    await p.click("[data-wf-tab=editor]");
    await p.click(".wf-flow-menu summary");
    await p.click("[data-action=flow-new]");
    assert.equal(
      await p
        .locator("[data-action=flow-publish],[data-workflow-confirm]")
        .count(),
      0,
    );
    assert.equal(
      await p.locator("[data-action=flow-save]").innerText(),
      "保存",
    );
    const name = "QA 单次保存 " + Date.now();
    await p.fill("[data-workflow-name]", name);
    await p.locator("[data-workflow-name]").blur();
    const savedResponse = p.waitForResponse(
      (r) =>
        r.url().endsWith("/workflow/save") && r.request().method() === "POST",
    );
    await p.click("[data-action=flow-save]");
    const response = await savedResponse;
    assert.equal(response.status(), 200);
    const saved = await response.json();
    await p.waitForFunction(
      () => !document.querySelector("[data-action=flow-save]").disabled,
    );
    const state = await p.evaluate(() =>
      fetch("/workflow/state").then((r) => r.json()),
    );
    assert.ok(state.published.some((f) => f.id === saved.id));
    assert.equal(
      await p.locator("[data-action=inspect-docs]").evaluate((e) => e.tagName),
      "A",
    );
    await p.click("[data-action=inspect-docs]");
    assert.equal(
      await p
        .locator("[data-action=inspect-docs]")
        .getAttribute("aria-selected"),
      "true",
    );
    await p.keyboard.press("ArrowLeft");
    assert.equal(
      await p
        .locator("[data-action=inspect-params]")
        .getAttribute("aria-selected"),
      "true",
    );
    await p.screenshot({
      path: ".scratch/workflow-deploy/editor-simplified.png",
    });
    await p.click("[data-wf-tab=entries]");
    await p.click("[data-action=entry-new]");
    await p.selectOption("[data-entry-flow]", saved.id);
    await p.fill("[data-wf-drawer] [data-field=name]", name);
    await p.uncheck("[data-wf-drawer] [data-field=confirm]");
    await p.screenshot({ path: ".scratch/workflow-deploy/entry-confirm.png" });
    await p.click("[data-action=drawer-save]");
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    const card = p.locator(".wf-entry").filter({ hasText: name });
    await card.waitFor();
    await card.locator(".action-menu summary").click();
    await card.locator("[data-action=entry-workflow]").click();
    assert.equal(await p.inputValue("[data-flow-select]"), saved.id);
    assert.equal(await p.locator(".wf-editor").isVisible(), true);
    await p.click("[data-wf-tab=entries]");
    await card.locator(".action-menu summary").click();
    await card.locator("[data-action=entry-edit]").click();
    assert.equal(await p.isChecked("[data-field=confirm]"), false);
    await p.check("[data-field=confirm]");
    await p.click("[data-action=drawer-save]");
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await p.waitForTimeout(200);
    let confirmCount = 0;
    p.once("dialog", (d) => {
      confirmCount++;
      d.dismiss();
    });
    await card.locator("[data-action=run]").click();
    assert.equal(confirmCount, 1);
    assert.equal(await card.isVisible(), true);
    await p.setViewportSize({ width: 390, height: 844 });
    await p.click("[data-wf-tab=editor]");
    assert.equal(
      await p.evaluate(() => document.documentElement.scrollWidth > innerWidth),
      false,
    );
    await p.screenshot({
      path: ".scratch/workflow-deploy/editor-simplified-mobile.png",
    });
    assert.equal(nav, 0);
    assert.deepEqual(errors, []);
    console.log(
      "SINGLE SAVE + ENTRY CONFIRM + ENTRY NAV + TABS PASS",
      saved.id,
    );
  } finally {
    await b.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
