const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 720 } });
    await page.setContent(`<!doctype html><html><body><main class="workspace kubernetes-page"><div class="kubernetes-table-shell"><table class="kubernetes-table"><thead><tr><th>Workload</th><th>Namespace</th><th>Status</th><th>Ready</th><th>Node</th><th>CPU</th><th>Memory</th><th>Restarts</th><th><span class="sr-only">Actions</span></th></tr></thead><tbody><tr><td><button class="kubernetes-workload-open"><strong>api</strong><small>Deployment · registry.example/api:v2</small></button></td><td><code>production</code></td><td><span class="kubernetes-state" data-state="ready"><i></i>Ready</span></td><td><strong>2 / 2</strong></td><td class="kubernetes-workload-node"><code title="edge-worker-01, edge-worker-02">edge-worker-01, edge-worker-02</code></td><td>200m</td><td>224 MiB</td><td>0</td><td><button type="button">Open</button></td></tr><tr><td>pending-job</td><td>jobs</td><td>Pending</td><td>0 / 1</td><td class="kubernetes-workload-node">—</td><td>0m</td><td>0 B</td><td>0</td><td></td></tr></tbody></table></div></main></body></html>`);
    const repository = path.resolve(__dirname, "../..");
    await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
    const nodeHeader = page.locator(".kubernetes-table th").nth(4);
    const nodeCell = page.locator(".kubernetes-workload-node").first();
    assert.equal(await nodeHeader.textContent(), "Node");
    assert.equal(await nodeCell.locator("code").getAttribute("title"), "edge-worker-01, edge-worker-02");
    assert.equal(await page.locator(".kubernetes-workload-node").nth(1).textContent(), "—");
    assert.equal(await nodeCell.isVisible(), true, "node placement should be visible in the desktop workload table");
    if (process.env.SCRIPTBOARD_KUBERNETES_NODE_SCREENSHOT) {
      await page.screenshot({ path: process.env.SCRIPTBOARD_KUBERNETES_NODE_SCREENSHOT, fullPage: true });
    }
    await page.setViewportSize({ width: 390, height: 720 });
    assert.equal(await nodeCell.isVisible(), false, "node placement should yield to core status columns on narrow screens");
    assert.equal(await page.locator(".kubernetes-table th").nth(8).isVisible(), true, "the action column must remain available on narrow screens");
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "the mobile workload table must not overflow horizontally");
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
