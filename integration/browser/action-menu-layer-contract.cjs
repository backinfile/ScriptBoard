"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 900, height: 700 } });
    const repository = path.resolve(__dirname, "../..");
    const failures = [];
    page.on("pageerror", error => failures.push(error.message));
    await page.route("http://scriptboard.test/**", route => route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html lang="en-US"><body>
        <main style="padding:80px">
          <section style="position:relative;z-index:1;transform:translateZ(0);overflow:hidden;height:50px">
            <details class="action-menu">
              <summary aria-label="More actions">...</summary>
              <div><button type="button">Edit item</button><button type="button">Delete item</button></div>
            </details>
          </section>
        </main>
        <div data-cover style="position:fixed;z-index:999999;top:120px;left:70px;width:260px;height:180px;background:#f00"></div>
      </body></html>`,
    }));
    await page.goto("http://scriptboard.test/harness");
    await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
    await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });

    await page.locator(".action-menu > summary").click();
    const panel = page.locator(".action-menu > div");
    await page.waitForFunction(() => {
      const menu = document.querySelector(".action-menu > div");
      return menu && menu.getBoundingClientRect().height > 0;
    });
    const layer = await panel.evaluate(menu => {
      const item = menu.querySelector("button");
      const bounds = item.getBoundingClientRect();
      const x = bounds.left + bounds.width / 2;
      const y = bounds.top + bounds.height / 2;
      return {
        topElement: document.elementFromPoint(x, y)?.textContent?.trim(),
        item: item.textContent.trim(),
        panelOpenInTopLayer: menu.matches(":popover-open"),
      };
    });
    assert.equal(layer.topElement, layer.item, `action menu was covered or clipped: ${JSON.stringify(layer)}`);
    assert.equal(layer.panelOpenInTopLayer, true, "action menu panel must use the browser top layer");
    assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
    process.stdout.write("Action menu layer contract passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
