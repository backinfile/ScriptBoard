const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
async function downloaded(d) {
  const stream = await d.createReadStream();
  let text = "";
  for await (const b of stream) text += b.toString("utf8");
  return text;
}
(async () => {
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const p = await browser.newPage({
        viewport: { width: 1500, height: 1000 },
      }),
      errors = [];
    p.on("pageerror", (e) => errors.push(e.message));
    await p.goto("http://127.0.0.1:18910/login");
    await p.fill("[name=username]", "admin");
    await p.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await p.locator("button[type=submit]").first().click();
    await p.waitForURL((u) => u.pathname != "/login");
    await p.goto("http://127.0.0.1:18910/workflow");
    for (const tab of ["editor", "history", "custom", "guide", "entries"]) {
      await p.click("[data-wf-tab=" + tab + "]");
      assert.equal(new URL(p.url()).searchParams.get("tab"), tab);
      await p.reload();
      await p.waitForSelector(
        '[data-wf-tab="' + tab + '"][aria-selected="true"]',
      );
    }
    await p.goto("http://127.0.0.1:18910/workflow?tab=invalid&keep=1");
    await p.waitForSelector("[data-wf-tab=entries][aria-selected=true]");
    assert.equal(new URL(p.url()).searchParams.get("tab"), "entries");
    assert.equal(new URL(p.url()).searchParams.get("keep"), "1");
    let nav = 0;
    p.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === p.mainFrame()) nav++;
    });
    await p.evaluate(() => {
      document.querySelector("[data-workflow]").dataset.historyTest =
        "preserved";
    });
    await p.click("[data-wf-tab=editor]");
    await p.click("[data-wf-tab=custom]");
    await p.goBack();
    await p.waitForSelector("[data-wf-tab=editor][aria-selected=true]");
    await p.goForward();
    await p.waitForSelector("[data-wf-tab=custom][aria-selected=true]");
    assert.equal(
      await p.locator("[data-workflow]").getAttribute("data-history-test"),
      "preserved",
    );
    await p.click("[data-wf-tab=guide]");
    let event = p.waitForEvent("download");
    await p.click("[data-action=guide-download]");
    const guide = await downloaded(await event);
    assert.match(guide, /内置节点模板/);
    assert.match(guide, /wait_for/);
    assert.match(guide, /customRevision/);
    event = p.waitForEvent("download");
    await p.click("[data-action=example-download]");
    const text = await downloaded(await event),
      bundle = JSON.parse(text);
    bundle.workflow.name = "QA 导入 " + Date.now();
    await p.screenshot({ path: ".scratch/workflow-deploy/workflow-guide.png" });
    assert.equal(await p.locator("[data-action=flow-import]").count(), 0);
    assert.equal(await p.locator("[data-action=flow-export]").count(), 0);
    await p.click("[data-wf-tab=editor]");
    await p.locator(".wf-flow-menu summary").click();
    await p.click("[data-action=flow-import]");
    await p.locator("[data-workflow-file]").setInputFiles({
      name: "example.workflow.json",
      mimeType: "application/json",
      buffer: Buffer.from(JSON.stringify(bundle)),
    });
    await p.waitForFunction(() =>
      document
        .querySelector("[data-field=text]")
        .value.includes("scriptboard.workflow"),
    );
    await p.screenshot({
      path: ".scratch/workflow-deploy/workflow-import.png",
    });
    const result = p.waitForResponse(
      (r) =>
        r.url().endsWith("/workflow/import") && r.request().method() === "POST",
    );
    await p.click("[data-action=drawer-save]");
    const response = await result;
    assert.equal(response.status(), 200);
    const imported = await response.json();
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await p.waitForSelector(".wf-flow-menu");
    await p.locator(".wf-flow-menu summary").click();
    event = p.waitForEvent("download");
    await p.click("[data-action=flow-export]");
    const exported = JSON.parse(await downloaded(await event));
    assert.deepEqual(exported.workflow.graph, bundle.workflow.graph);
    assert.equal(exported.workflow.id, "");
    assert.deepEqual(exported.customNodes, []);
    const status = await p.evaluate(async (id) => {
      const api = async (path, v) => {
        const r = await fetch("/workflow/" + path, {
          method: v ? "POST" : "GET",
          headers: {
            "Content-Type": "application/json",
            "X-CSRF-Token":
              document.querySelector("[data-workflow]").dataset.csrf,
          },
          body: v ? JSON.stringify(v) : undefined,
        });
        if (!r.ok) throw Error(await r.text());
        return r.json();
      };
      const e = await api("entry", {
        name: "QA 导入示例执行",
        workflowId: id,
        workflowRevision: 1,
        values: {},
        confirm: false,
      });
      const r = await api("start", { id: e.id });
      for (let i = 0; i < 40; i++) {
        const run = await api("runs/" + r.id);
        if (!["queued", "running"].includes(run.status)) return run;
        await new Promise((r) => setTimeout(r, 200));
      }
      throw Error("timeout");
    }, imported.id);
    assert.equal(status.status, "succeeded");
    assert.equal(status.result.result, 14);
    await p.locator(".wf-flow-menu summary").click();
    await p.click("[data-action=flow-import]");
    const before = await p.evaluate(() =>
      fetch("/workflow/state")
        .then((r) => r.json())
        .then((s) => s.draft.length),
    );
    bundle.workflow.graph.dependencies.push({ from: "e", to: "s" });
    await p.fill("[data-field=text]", JSON.stringify(bundle));
    await p.click("[data-action=drawer-save]");
    await p.waitForSelector(".wf-form-error");
    assert.equal(
      await p.locator("[data-wf-drawer]").evaluate((e) => e.open),
      true,
    );
    assert.equal(
      await p.evaluate(() =>
        fetch("/workflow/state")
          .then((r) => r.json())
          .then((s) => s.draft.length),
      ),
      before,
    );
    p.once("dialog", (d) => d.accept());
    await p.click("[data-action=drawer-close]");
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await p.setViewportSize({ width: 390, height: 844 });
    await p.click("[data-wf-tab=guide]");
    assert.equal(
      await p.evaluate(() => document.documentElement.scrollWidth > innerWidth),
      false,
    );
    assert.equal(nav, 0);
    assert.deepEqual(errors, []);
    console.log(
      "GUIDE DOWNLOAD + IMPORT EXPORT + RUN + INVALID ATOMICITY PASS",
      imported.id,
    );
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
