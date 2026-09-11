const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
(async () => {
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const p = await browser.newPage({
      viewport: { width: 1500, height: 1000 },
    });
    const errors = [];
    p.on("pageerror", (e) => errors.push(e.message));
    await p.goto("http://127.0.0.1:18910/login");
    await p.fill("[name=username]", "admin");
    await p.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await p.locator("button[type=submit]").first().click();
    await p.waitForTimeout(400);
    await p.goto("http://127.0.0.1:18910/workflow");
    await p.click("[data-wf-tab=custom]");
    let nav = 0;
    p.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === p.mainFrame()) nav++;
    });
    await p.click("[data-action=custom-new]");
    assert.equal(await p.locator("[data-action=custom-delete]").count(), 0);
    const name = "QA 节点操作区 " + Date.now();
    await p.fill("[data-wf-drawer] [data-field=name]", name);
    await p.click("[data-action=drawer-save]");
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    const card = p.locator(".wf-card").filter({ hasText: name });
    await card.waitFor();
    assert.equal(await card.locator("[data-action=custom-delete]").count(), 0);
    const more = card.locator("[data-action=custom-edit]");
    assert.equal(await more.innerText(), "");
    const [cr, mr] = await Promise.all([
      card.boundingBox(),
      more.boundingBox(),
    ]);
    assert.ok(
      mr.x > cr.x + cr.width / 2 && mr.y > cr.y + cr.height / 2,
      "more button bottom right",
    );
    await p.screenshot({ path: ".scratch/workflow-deploy/custom-more.png" });
    await more.click();
    await p.waitForSelector(".wf-custom-danger");
    assert.equal(
      await p
        .locator("[data-wf-drawer]>section")
        .evaluate((e) =>
          e.lastElementChild.classList.contains("wf-custom-danger"),
        ),
      true,
    );
    const del = p.locator("[data-action=custom-delete]");
    await del.scrollIntoViewIfNeeded();
    await p.screenshot({
      path: ".scratch/workflow-deploy/custom-delete-zone.png",
    });
    p.once("dialog", (d) => d.dismiss());
    await del.click();
    assert.equal(
      await p.locator("[data-wf-drawer]").evaluate((e) => e.open),
      true,
    );
    assert.equal(await card.count(), 1);
    await p.setViewportSize({ width: 390, height: 844 });
    assert.equal(
      await p
        .locator("[data-wf-drawer]>section")
        .evaluate((e) => e.scrollWidth > e.clientWidth),
      false,
    );
    p.once("dialog", (d) => d.accept());
    await del.click();
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await card.waitFor({ state: "detached" });
    assert.equal(nav, 0);
    assert.deepEqual(errors, []);
    console.log("CUSTOM CARD MORE + DRAWER DELETE PASS");
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
