// Requires an existing local test deployment. Retains the QA draft and screenshots.

const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const assert = require("node:assert/strict"),
  fs = require("fs");
(async () => {
  fs.mkdirSync(".scratch/workflow-deploy", { recursive: true });
  if (!process.env.WORKFLOW_TEST_PASSWORD)
    throw new Error("Set WORKFLOW_TEST_PASSWORD");
  const browser = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const page = await browser.newPage({
        viewport: { width: 1600, height: 1000 },
      }),
      errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.on("dialog", (d) => d.accept());
    await page.goto(
      (process.env.WORKFLOW_TEST_URL || "http://127.0.0.1:18910") + "/login",
    );
    await page.fill(
      "[name=username]",
      process.env.WORKFLOW_TEST_USER || "admin",
    );
    await page.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await page.locator("button[type=submit]").first().click();
    await page.waitForTimeout(500);
    await page.goto(
      (process.env.WORKFLOW_TEST_URL || "http://127.0.0.1:18910") + "/workflow",
    );
    const id = await page.evaluate(async () => {
      const fields = [{ name: "value", type: "string", required: false }];
      const nodes = [
        { id: "s", kind: "start", name: "入口", inputs: [], outputs: fields },
        {
          id: "a",
          kind: "script",
          name: "脚本任务",
          inputs: fields,
          outputs: fields,
          config: { code: "echo test" },
        },
        {
          id: "b",
          kind: "script",
          name: "并行任务",
          inputs: fields,
          outputs: fields,
          config: { code: "echo test" },
        },
        { id: "e", kind: "end", name: "出口", inputs: fields, outputs: [] },
        {
          id: "v",
          kind: "literal",
          name: "固定值",
          inputs: [],
          outputs: fields,
          config: { value: "test" },
        },
      ].map((n, i) => ({
        ...n,
        x: 500 - i * 70,
        y: i * 25,
        values: n.values || {},
        config: n.config || {},
      }));
      const d = {
        name: "QA 分层整理",
        parameters: fields,
        graph: {
          nodes,
          links: [
            {
              from: { node: "s", field: "value" },
              to: { node: "a", field: "value" },
            },
            {
              from: { node: "s", field: "value" },
              to: { node: "b", field: "value" },
            },
            {
              from: { node: "a", field: "value" },
              to: { node: "e", field: "value" },
            },
          ],
          dependencies: [
            { from: "s", to: "a" },
            { from: "s", to: "b" },
            { from: "a", to: "e" },
            { from: "b", to: "e" },
          ],
        },
      };
      const r = await fetch("/workflow/draft", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token":
            document.querySelector("[data-workflow]").dataset.csrf,
        },
        body: JSON.stringify(d),
      });
      if (!r.ok) throw Error(await r.text());
      return (await r.json()).id;
    });
    // A fresh page includes the retained fixture; all subsequent interactions stay in-page.
    await page.goto(
      (process.env.WORKFLOW_TEST_URL || "http://127.0.0.1:18910") + "/workflow",
    );
    let navigations = 0;
    page.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === page.mainFrame()) navigations++;
    });
    await page.click("[data-wf-tab=editor]");
    await page.selectOption("[data-flow-select]", id);
    const positions = () =>
      page
        .locator(".wf-node")
        .evaluateAll((els) =>
          els.map((e) => ({
            id: e.dataset.node,
            x: parseFloat(e.style.left),
            y: parseFloat(e.style.top),
            w: e.offsetWidth,
            h: e.offsetHeight,
          })),
        );
    const before = await positions();
    async function arrange() {
      await page.click("[data-action=arrange]");
      await page.waitForFunction(
        () => !document.querySelector("[data-action=arrange]").disabled,
      );
      assert.match(
        await page.locator("[data-wf-toast]").textContent(),
        /已按依赖/,
      );
    }
    await arrange();
    const after = await positions();
    assert.notDeepEqual(after, before);
    for (let i = 0; i < after.length; i++)
      for (let j = i + 1; j < after.length; j++) {
        const a = after[i],
          b = after[j];
        assert(
          a.x + a.w <= b.x ||
            b.x + b.w <= a.x ||
            a.y + a.h <= b.y ||
            b.y + b.h <= a.y,
          "nodes overlap",
        );
      }
    assert(
      after.find((n) => n.id === "s").x < after.find((n) => n.id === "a").x,
    );
    assert(
      after.find((n) => n.id === "a").x < after.find((n) => n.id === "e").x,
    );
    await arrange();
    assert.deepEqual(await positions(), after, "repeat stability");
    await page.click("[data-action=undo]");
    assert.deepEqual(await positions(), before, "one undo restores positions");
    await page.click("[data-action=redo]");
    assert.deepEqual(await positions(), after);
    await page.click("[data-action=flow-save]");
    await page.waitForTimeout(350);
    const saved = await page.evaluate(
      (id) =>
        fetch("/workflow/state")
          .then((r) => r.json())
          .then((s) => s.draft.find((d) => d.id === id)),
      id,
    );
    assert.equal(saved.graph.links.length, 3);
    assert.equal(saved.graph.dependencies.length, 4);
    assert.equal(
      saved.graph.nodes.find((n) => n.id === "a").config.code,
      "echo test",
    );
    assert.equal(await page.locator("h1").textContent(), "工作流");
    const bounds = await page.locator("[data-workflow]").boundingBox();
    assert(
      bounds.x + bounds.width >=
        (await page.evaluate(
          () => document.body.getBoundingClientRect().right,
        )) -
          1,
    );
    assert(bounds.y + bounds.height <= 1001);
    await page.screenshot({
      path: ".scratch/workflow-deploy/layout-desktop.png",
    });
    for (const tab of ["entries", "history", "custom"]) {
      await page.click("[data-wf-tab=" + tab + "]");
      await page.screenshot({
        path: ".scratch/workflow-deploy/layout-" + tab + ".png",
      });
    }
    await page.click("[data-wf-tab=editor]");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(600);
    await page.click("[data-action=fit]");
    await page.screenshot({
      path: ".scratch/workflow-deploy/layout-mobile.png",
    });
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      "horizontal page overflow",
    );
    await page.setViewportSize({ width: 1600, height: 1000 });
    const stable = await positions();
    await page.evaluate(() => {
      window.RealELK = window.ELK;
      window.ELK = class extends window.RealELK {
        async layout(g) {
          await new Promise((r) => setTimeout(r, 350));
          return super.layout(g);
        }
      };
    });
    await page.click("[data-action=arrange]");
    await page.fill("[data-workflow-name]", "QA 整理期间编辑");
    await page.waitForFunction(
      () => !document.querySelector("[data-action=arrange]").disabled,
    );
    assert.deepEqual(await positions(), stable);
    assert.equal(
      await page.inputValue("[data-workflow-name]"),
      "QA 整理期间编辑",
    );
    await page.evaluate(() => {
      window.ELK = class extends window.RealELK {
        layout() {
          return Promise.reject(new Error("QA layout failure"));
        }
      };
    });
    await page.click("[data-action=arrange]");
    await page.waitForFunction(
      () => !document.querySelector("[data-action=arrange]").disabled,
    );
    assert.deepEqual(await positions(), stable);
    assert.match(
      await page.locator("[data-wf-toast]").textContent(),
      /QA layout failure/,
    );
    assert.equal(navigations, 0);
    assert.deepEqual(errors, []);
    fs.writeFileSync(
      ".scratch/workflow-deploy/layout-results.json",
      JSON.stringify(
        {
          passed: true,
          navigations,
          errors,
          id,
          checks: [
            "layer ordering",
            "variable dimensions and disconnected node",
            "no overlap",
            "stable repeat",
            "one-step undo/redo",
            "save retains edges and script",
            "single title",
            "full width and viewport height",
            "all tabs",
            "mobile overflow",
            "no HTML refresh",
          ],
        },
        null,
        2,
      ),
    );
    console.log("LAYOUT UI PASS");
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
