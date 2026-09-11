const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
(async () => {
  const b = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const p = await b.newPage({ viewport: { width: 1500, height: 1000 } });
    p.on("dialog", (d) => d.accept());
    const errors = [];
    p.on("pageerror", (e) => errors.push(e.message));
    await p.goto("http://127.0.0.1:18910/login");
    await p.fill("[name=username]", "admin");
    await p.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await p.locator("button[type=submit]").first().click();
    await p.waitForURL((u) => u.pathname != "/login");
    await p.goto("http://127.0.0.1:18910/workflow");
    await p.click("[data-wf-tab=editor]");
    await p.click(".wf-flow-menu summary");
    await p.click("[data-action=flow-new]");
    let nav = 0;
    p.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === p.mainFrame()) nav++;
    });
    await p.click("[data-action=node-search]");
    await p.fill("[data-node-query]", "Go 构建");
    await p.locator("[data-action=choose-node]").first().click();
    await p.click("[data-action=arrange]");
    await p.waitForFunction(
      () => !document.querySelector("[data-action=arrange]").disabled,
    );
    const go = p
      .locator(".wf-node")
      .filter({ has: p.locator("header small", { hasText: /^go$/ }) });
    assert.equal(await go.locator(".wf-node-param.in").count(), 8);
    assert.equal(await go.locator("[data-node-value]").count(), 8);
    assert.equal(await go.locator(".wf-node-widget").count(), 0);
    for (const f of [
      "directory",
      "package",
      "binary",
      "goos",
      "goarch",
      "cgo",
      "ldflags",
      "version",
    ]) {
      const cell = go
        .locator(".wf-node-param.in")
        .filter({ has: p.locator('[data-node-value="' + f + '"]') });
      assert.equal(await cell.count(), 1);
      assert.equal(await cell.locator(".wf-port").count(), 1);
    }
    await go.locator("[data-node-value=directory]").fill("D:/qa");
    await go.locator("[data-node-value=directory]").blur();
    await go.screenshot({
      path: ".scratch/workflow-deploy/node-input-unified.png",
    });
    const points = await p.locator("[data-port]").evaluateAll((es) =>
      ["version", "directory"].map((field, i) => {
        const el = es.find((e) => {
          const x = JSON.parse(e.dataset.port);
          return (
            x.mode === "data" &&
            x.dir === (i ? "in" : "out") &&
            x.field === field
          );
        });
        const r = el.getBoundingClientRect();
        return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
      }),
    );
    await p.mouse.move(points[0].x, points[0].y);
    await p.mouse.down();
    await p.mouse.move(points[1].x, points[1].y, { steps: 10 });
    await p.mouse.up();
    assert.equal(await go.locator("[data-node-value=directory]").count(), 0);
    assert.match(await go.locator(".wf-param-source").innerText(), /version/);
    const distance = await p.locator(".wf-link-hit").evaluate((el) => {
      const point = el.getPointAtLength(el.getTotalLength()),
        m = el.getScreenCTM();
      const socket = [...document.querySelectorAll("[data-port]")]
        .find((e) => {
          const p = JSON.parse(e.dataset.port);
          return p.dir === "in" && p.field === "directory";
        })
        .getBoundingClientRect();
      return Math.hypot(
        point.x * m.a + point.y * m.c + m.e - (socket.x + socket.width / 2),
        point.x * m.b + point.y * m.d + m.f - (socket.y + socket.height / 2),
      );
    });
    assert.ok(distance < 2, "wire aligned: " + distance);
    await p.click("[data-action=undo]");
    assert.equal(
      await go.locator("[data-node-value=directory]").inputValue(),
      "D:/qa",
    );
    assert.equal(nav, 0);
    assert.deepEqual(errors, []);
    console.log("UNIFIED INPUT ROW + SOURCE + ANCHOR + UNDO PASS");
  } finally {
    await b.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
