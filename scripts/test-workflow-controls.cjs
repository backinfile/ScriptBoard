const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
(async () => {
  const b = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const p = await b.newPage({ viewport: { width: 1500, height: 1000 } });
    const errors = [];
    p.on("pageerror", (e) => errors.push(e.message));
    await p.goto("http://127.0.0.1:18910/login");
    await p.fill("[name=username]", "admin");
    await p.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await p.locator("button[type=submit]").first().click();
    await p.waitForTimeout(400);
    await p.goto("http://127.0.0.1:18910/workflow");
    await p.waitForSelector("[data-wf-tab=editor]");
    const fixture = await p.evaluate(async () => {
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
      const f = (name, type, value) => ({
        name,
        type,
        required: true,
        ...(value !== undefined ? { default: value } : {}),
      });
      const node = (id, kind, inputs = [], outputs = [], config = {}) => ({
        id,
        kind,
        name: id,
        x: 50,
        y: 50,
        inputs,
        outputs,
        values: {},
        config,
      });
      let d = await api("draft", {
        name: "QA 算术与分支 " + Date.now(),
        parameters: [],
        graph: {
          nodes: [
            node("s", "start"),
            node(
              "calc",
              "arithmetic",
              [f("a", "number", 7), f("b", "number", 2)],
              [f("result", "number")],
              { operation: "multiply" },
            ),
            node(
              "branch",
              "switch",
              [f("value", "json")],
              [f("matched", "json")],
              {
                valueType: "integer",
                matchMode: "first",
                rules: [
                  { id: "large", name: "大于十", operator: "gt", value: 10 },
                ],
              },
            ),
            node("yes", "merge"),
            node("no", "merge"),
            node("join", "merge", [], [], { joinMode: "any" }),
            node("e", "end", [f("result", "number")]),
          ],
          links: [
            {
              from: { node: "calc", field: "result" },
              to: { node: "branch", field: "value" },
            },
            {
              from: { node: "calc", field: "result" },
              to: { node: "e", field: "result" },
            },
          ],
          dependencies: [
            { from: "s", to: "calc" },
            { from: "branch", to: "yes", outlet: "large" },
            { from: "branch", to: "no", outlet: "default" },
            { from: "yes", to: "join" },
            { from: "no", to: "join" },
            { from: "join", to: "e" },
          ],
        },
      });
      d = await api("publish", { id: d.id, revision: d.revision });
      const entry = await api("entry", {
        name: d.name,
        workflowId: d.id,
        workflowRevision: d.revision,
        values: {},
      });
      const run = await api("start", { id: entry.id });
      return { id: d.id, run: run.id };
    });
    await p.goto("http://127.0.0.1:18910/workflow");
    let nav = 0;
    p.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === p.mainFrame()) nav++;
    });
    await p.click("[data-wf-tab=editor]");
    await p.selectOption("[data-flow-select]", fixture.id);
    await p.click("[data-action=arrange]");
    await p.waitForFunction(
      () => !document.querySelector("[data-action=arrange]").disabled,
    );
    await p.locator("[data-node=branch] header").click();
    await p.click("[data-action=branch-rules]");
    await p.waitForSelector('[data-rule="0:name"]');
    await p.fill('[data-rule="0:name"]', "大于十（主分支）");
    await p.click("[data-action=rule-add]");
    await p.fill('[data-rule="1:name"]', "等于十");
    await p.fill('[data-rule="1:value"]', "10");
    await p.click('[data-action=rule-up][data-id="1"]');
    await p.screenshot({ path: ".scratch/workflow-deploy/control-rules.png" });
    await p.click("[data-action=drawer-save]");
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await p.waitForFunction(() =>
      document
        .querySelector("[data-node=branch]")
        ?.textContent.includes("大于十（主分支）"),
    );
    assert.equal(
      await p.locator("[data-node=branch] [data-outlet=large]").count(),
      1,
    );
    assert.match(
      await p.locator("[data-node=branch]").textContent(),
      /大于十（主分支）/,
    );
    await p.screenshot({ path: ".scratch/workflow-deploy/control-editor.png" });
    const run = await p.evaluate(async (id) => {
      for (let i = 0; i < 30; i++) {
        const r = await (await fetch("/workflow/runs/" + id)).json();
        if (["succeeded", "failed"].includes(r.status)) return r;
        await new Promise((r) => setTimeout(r, 200));
      }
      throw Error("run timeout");
    }, fixture.run);
    assert.equal(run.status, "succeeded");
    assert.equal(run.result.result, 14);
    assert.equal(run.steps.find((s) => s.nodeId === "no").status, "skipped");
    assert.ok(
      run.steps.find((s) => s.nodeId === "branch").outlets.includes("large"),
    );
    await p.click("[data-wf-tab=history]");
    await p.locator(".wf-run summary").first().click();
    await p
      .locator(".wf-run[open] [data-action=history-step]")
      .filter({ hasText: "branch" })
      .click();
    assert.match(await p.locator(".wf-run[open]").textContent(), /已触发/);
    await p.screenshot({
      path: ".scratch/workflow-deploy/control-history.png",
    });
    assert.equal(nav, 0);
    assert.deepEqual(errors, []);
    console.log("CONTROL UI + ARITHMETIC RUN PASS", fixture);
  } finally {
    await b.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
