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
    await p.goto("http://127.0.0.1:18910/login");
    await p.fill("[name=username]", "admin");
    await p.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await p.locator("button[type=submit]").first().click();
    await p.waitForURL((u) => u.pathname != "/login");
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
      const n = (id, kind, inputs = []) => ({
        id,
        kind,
        name: id,
        x: 20,
        y: 20,
        inputs,
        outputs: [],
        config: {},
        values: {},
      });
      const d = await api("save", {
        name: "QA 等待节点 " + Date.now(),
        parameters: [],
        graph: {
          nodes: [
            n("s", "start"),
            n("timer", "wait", [
              {
                name: "seconds",
                type: "number",
                default: 0.05,
                required: true,
              },
            ]),
            n("waiter", "wait_for"),
            n("after", "merge"),
            n("e", "end"),
          ],
          links: [],
          dependencies: [
            { from: "s", to: "timer" },
            { from: "timer", to: "waiter", outlet: "always" },
            { from: "waiter", to: "after" },
            { from: "after", to: "e" },
          ],
        },
      });
      const e = await api("entry", {
          name: d.name,
          workflowId: d.id,
          workflowRevision: 1,
          confirm: false,
          values: {},
        }),
        run = await api("start", { id: e.id });
      for (let i = 0; i < 30; i++) {
        const r = await api("runs/" + run.id);
        if (r.status === "succeeded") return { id: d.id, run: r };
        if (r.status === "failed") throw Error(r.error);
        await new Promise((r) => setTimeout(r, 200));
      }
      throw Error("timeout");
    });
    const timer = fixture.run.steps.find((s) => s.nodeId === "timer"),
      waiter = fixture.run.steps.find((s) => s.nodeId === "waiter");
    assert.ok(waiter.attempts[0].startedAt >= timer.attempts[0].finishedAt);
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
    await p.locator("[data-node=waiter] header").click();
    await p.click("[data-action=wait-targets]");
    assert.equal(await p.isChecked("[data-wait-target=timer]"), true);
    await p.check("[data-wait-target=after]");
    await p.click("[data-action=drawer-save]");
    assert.match(await p.locator(".wf-form-error").innerText(), /循环/);
    await p.uncheck("[data-wait-target=after]");
    await p.check("[data-wait-target=s]");
    await p.screenshot({ path: ".scratch/workflow-deploy/wait-targets.png" });
    await p.click("[data-action=drawer-save]");
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await p.waitForTimeout(200);
    assert.equal(await p.locator(".wf-link-hit").count(), 5);
    await p.click("[data-action=flow-save]");
    await p.waitForFunction(
      () => !document.querySelector("[data-action=flow-save]").disabled,
    );
    assert.equal(nav, 0);
    console.log("WAIT TIMER + NODE COMPLETION + TARGETS + CYCLE PASS");
  } finally {
    await b.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
