const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
(async () => {
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const page = await browser.newPage({
      viewport: { width: 1500, height: 1000 },
    });
    page.on("dialog", (d) => d.accept());
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto("http://127.0.0.1:18910/login");
    await page.fill("[name=username]", "admin");
    await page.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await page.locator("button[type=submit]").first().click();
    await page.waitForURL((url) => url.pathname !== "/login");
    await page.goto("http://127.0.0.1:18910/workflow");
    await page.click("[data-wf-tab=editor]");
    await page.click(".wf-flow-menu summary");
    await page.click("[data-action=flow-new]");
    let nav = 0;
    page.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === page.mainFrame()) nav++;
    });
    const dragFromStart = async () => {
      const port = await page.locator("[data-port]").evaluateAll((es) => {
        const el = es.find((e) => {
          const p = JSON.parse(e.dataset.port);
          return p.mode === "data" && p.dir === "out" && p.field === "version";
        });
        const r = el.getBoundingClientRect();
        return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
      });
      const stage = await page.locator(".wf-stage").boundingBox();
      await page.mouse.move(port.x, port.y);
      await page.mouse.down();
      await page.mouse.move(
        stage.x + stage.width * 0.6,
        stage.y + stage.height * 0.75,
        { steps: 10 },
      );
      await page.mouse.up();
      assert.equal(
        await page.locator("[data-wf-search]").evaluate((e) => e.open),
        true,
        "drag opens picker",
      );
    };
    const closed = async () => {
      await page.waitForTimeout(100);
      assert.equal(
        await page.locator("[data-wf-search]").evaluate((e) => e.open),
        false,
        "node picker must close",
      );
      assert.equal(await page.locator(".wf-node").count(), 2);
      assert.equal(await page.locator(".wf-link-hit").count(), 0);
    };
    await dragFromStart();
    await page.mouse.click(270, 200);
    await closed();
    await dragFromStart();
    await page.keyboard.press("Escape");
    await closed();
    await dragFromStart();
    await page.click("[data-action=node-search-close]");
    await closed();
    await page.click("[data-action=node-search]");
    await page.fill("[data-node-query]", "Git");
    await page.locator("[data-action=choose-node]").first().click();
    assert.equal(await page.locator(".wf-node").count(), 3);
    assert.equal(
      await page.locator(".wf-link-hit").count(),
      0,
      "cancelled wire must not attach on next create",
    );
    await page.click("[data-action=arrange]");
    await page.waitForFunction(
      () => !document.querySelector("[data-action=arrange]").disabled,
    );
    const portPoint = async (dir, field) =>
      page.locator("[data-port]").evaluateAll(
        (es, query) => {
          const el = es.find((e) => {
            const p = JSON.parse(e.dataset.port);
            return (
              p.mode === "data" &&
              p.dir === query.dir &&
              p.field === query.field
            );
          });
          const r = el.getBoundingClientRect();
          return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
        },
        { dir, field },
      );
    const a = await portPoint("out", "version"),
      b = await portPoint("in", "repository");
    await page.mouse.move(a.x, a.y);
    await page.mouse.down();
    await page.mouse.move(b.x, b.y, { steps: 10 });
    await page.mouse.up();
    assert.equal(await page.locator(".wf-link-hit").count(), 1);
    const original = await page.locator(".wf-link-hit").getAttribute("d");
    const stage = await page.locator(".wf-stage").boundingBox();
    await page.mouse.move(b.x, b.y);
    await page.mouse.down();
    await page.mouse.move(
      stage.x + stage.width - 40,
      stage.y + stage.height - 40,
      { steps: 10 },
    );
    await page.mouse.up();
    assert.equal(
      await page.locator("[data-wf-search]").evaluate((e) => e.open),
      true,
    );
    await page.click("[data-action=node-search-close]");
    assert.equal(
      await page.locator(".wf-link-hit").count(),
      1,
      "cancelled rewire preserves edge",
    );
    assert.equal(
      await page.locator(".wf-link-hit").getAttribute("d"),
      original,
    );
    await page.click("[data-action=node-search]");
    await page.fill("[data-node-query]", "算术");
    await page.keyboard.press("Enter");
    assert.equal(
      await page.locator(".wf-node").count(),
      4,
      "Enter still creates node",
    );
    assert.equal(await page.locator(".wf-link-hit").count(), 1);
    assert.equal(nav, 0);
    assert.deepEqual(errors, []);
    console.log("NODE PICKER DISMISS PASS");
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
