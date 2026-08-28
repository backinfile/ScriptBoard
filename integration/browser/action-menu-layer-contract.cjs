"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 900, height: 320 } });
    const repository = path.resolve(__dirname, "../..");
    const failures = [];
    page.on("pageerror", error => failures.push(error.message));
    await page.route("http://scriptboard.test/**", route => route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html lang="en-US"><body>
        <main style="padding:260px 80px 0">
          <section style="position:relative;z-index:1;transform:translateZ(0);overflow:hidden;height:50px">
            <ol class="qr-grid"><li><details class="action-menu">
              <summary aria-label="More actions">...</summary>
              <div>${Array.from({ length: 12 }, (_, index) => `<button type="button">Action ${index + 1}</button>`).join("")}</div>
            </details></li></ol>
          </section>
        </main>
        <div data-cover style="position:fixed;z-index:999999;top:10px;left:70px;width:260px;height:220px;background:#f00"></div>
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

    const scrollGeometry = await panel.evaluate(menu => ({
      scrollHeight: menu.scrollHeight,
      clientHeight: menu.clientHeight,
      scrollTop: menu.scrollTop,
    }));
    assert.ok(scrollGeometry.scrollHeight > scrollGeometry.clientHeight, `action menu must overflow in the short viewport: ${JSON.stringify(scrollGeometry)}`);
    await panel.evaluate(menu => {
      menu.dataset.styleMutationsDuringScroll = "0";
      new MutationObserver(records => {
        const mutations = Number(menu.dataset.styleMutationsDuringScroll || 0) + records.length;
        menu.dataset.styleMutationsDuringScroll = String(mutations);
      }).observe(menu, { attributes: true, attributeFilter: ["style"] });
    });
    await panel.hover();
    await page.mouse.wheel(0, 1000);
    await page.waitForFunction(() => document.querySelector(".action-menu > div")?.scrollTop > 0);
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const draggedScroll = await panel.evaluate(menu => ({
      scrollTop: menu.scrollTop,
      maxScrollTop: menu.scrollHeight - menu.clientHeight,
      styleMutations: Number(menu.dataset.styleMutationsDuringScroll || 0),
    }));
    assert.ok(draggedScroll.scrollTop >= draggedScroll.maxScrollTop * 0.8, `action menu must scroll through its range: ${JSON.stringify(draggedScroll)}`);
    assert.equal(draggedScroll.styleMutations, 0, `scrolling the menu must not rewrite its geometry and interrupt a scrollbar drag: ${JSON.stringify(draggedScroll)}`);
    assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
    process.stdout.write("Action menu layer contract passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
