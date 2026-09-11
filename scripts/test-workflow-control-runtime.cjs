const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
(async () => {
  const b = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const p = await b.newPage();
    await p.goto("http://127.0.0.1:18910/login");
    await p.fill("[name=username]", "admin");
    await p.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await p.locator("button[type=submit]").first().click();
    await p.waitForTimeout(400);
    await p.goto("http://127.0.0.1:18910/workflow");
    await p.waitForSelector("[data-workflow]");
    const result = await p.evaluate(async () => {
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
      const f = { name: "answer", type: "number", required: true };
      const marker = "qa_retry_" + Date.now() + ".txt";
      const c = await api("custom", {
        name: "QA 首次失败后重试 " + Date.now(),
        language: "python",
        inputs: [],
        outputs: [f],
        code:
          'def main(inputs):\n    from pathlib import Path\n    p = Path("' +
          marker +
          '")\n    print("attempt marker", p.exists())\n    if not p.exists():\n        p.write_text("first attempt failed")\n        raise RuntimeError("retry fixture failure")\n    return {"answer": 42}\n',
      });
      const node = (id, kind, inputs = [], outputs = [], config = {}) => ({
        id,
        name: id,
        kind,
        x: 0,
        y: 0,
        inputs,
        outputs,
        config,
      });
      const start = async (name, graph) => {
        let d = await api("draft", { name, parameters: [], graph });
        d = await api("publish", { id: d.id, revision: d.revision });
        const e = await api("entry", {
          name,
          workflowId: d.id,
          workflowRevision: d.revision,
          values: {},
        });
        return api("start", { id: e.id });
      };
      const wait = async (id) => {
        for (let i = 0; i < 150; i++) {
          const r = await api("runs/" + id);
          if (!["queued", "running", "cancelling"].includes(r.status)) return r;
          await new Promise((r) => setTimeout(r, 200));
        }
        throw Error("timeout");
      };
      const run = await start(c.name, {
        nodes: [
          node("s", "start"),
          {
            ...node("retry", "custom", [], [f], {
              directory:
                "D:/Github/worktrees/ScriptBoard/workflow-design-20260909/.scratch/workflow-jobs",
              retryAttempts: 3,
              retryDelayMs: 100,
            }),
            customId: c.id,
            customRevision: c.revision,
          },
          node("e", "end", [f]),
        ],
        links: [
          {
            from: { node: "retry", field: "answer" },
            to: { node: "e", field: "answer" },
          },
        ],
        dependencies: [{ from: "s", to: "retry" }],
      });
      const retry = await wait(run.id);
      const attempts = retry.steps.find((s) => s.nodeId === "retry").attempts;
      const logs = [];
      for (const a of attempts || [])
        if (a.processId) logs.push(await api("logs/" + a.processId));
      const w = await start("QA 等待取消 " + Date.now(), {
        nodes: [
          node("s", "start"),
          node("wait", "wait", [
            { name: "seconds", type: "number", default: 30, required: true },
          ]),
          node("e", "end"),
        ],
        links: [],
        dependencies: [
          { from: "s", to: "wait" },
          { from: "wait", to: "e" },
        ],
      });
      for (let i = 0; i < 25; i++) {
        const r = await api("runs/" + w.id);
        if (r.steps.find((s) => s.nodeId === "wait").status === "dispatching")
          break;
        await new Promise((r) => setTimeout(r, 200));
      }
      await api("cancel", { id: w.id });
      const cancelled = await wait(w.id);
      return { retry, cancelled, logs };
    });
    assert.equal(result.retry.status, "succeeded", result.retry.error);
    assert.equal(result.retry.result.answer, 42);
    assert.equal(
      result.retry.steps.find((s) => s.nodeId === "retry").attempts.length,
      2,
    );
    assert.equal(result.logs.length, 2);
    assert.ok(result.logs.every((l) => l.length));
    assert.equal(result.cancelled.status, "cancelled");
    console.log(
      "REAL RETRY + ATTEMPT LOGS + WAIT CANCEL PASS",
      result.retry.id,
      result.cancelled.id,
    );
  } finally {
    await b.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
