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
        <main style="padding:120px">
          <section data-clip style="position:relative;z-index:1;transform:translateZ(0);overflow:hidden;height:42px">
            <button class="icon-button icon-tooltip" type="button" aria-label="Copy value" data-tooltip="Copy value">
              <span data-lucide="copy" aria-hidden="true"></span>
            </button>
          </section>
        </main>
        <div data-cover style="position:fixed;z-index:999999;top:70px;left:100px;width:220px;height:40px;background:#f00"></div>
        <dialog data-dialog>
          <button class="icon-button icon-tooltip" type="button" aria-label="Copy error details" data-tooltip="Copy error details">
            <span data-lucide="copy" aria-hidden="true"></span>
          </button>
        </dialog>
      </body></html>`,
    }));
    await page.goto("http://scriptboard.test/harness");
    await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
    await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });

    const button = page.locator("main .icon-tooltip");
    const tooltip = page.locator("#icon-tooltip-layer");
    await button.hover();
    await page.waitForFunction(() => {
      const element = document.querySelector("#icon-tooltip-layer");
      return element?.dataset.visible === "true" && getComputedStyle(element).opacity === "1";
    });
    const layer = await tooltip.evaluate(element => {
      const clip = document.querySelector("[data-clip]").getBoundingClientRect();
      const bounds = element.getBoundingClientRect();
      return {
        text: element.textContent,
        openInTopLayer: element.matches(":popover-open"),
        escapesClippedAncestor: bounds.top < clip.top,
        opacity: getComputedStyle(element).opacity,
      };
    });
    assert.equal(layer.text, "Copy value");
    assert.equal(layer.openInTopLayer, true, "copy tooltip must use the browser top layer");
    assert.equal(layer.escapesClippedAncestor, true, "copy tooltip must escape clipped containers");
    assert.equal(layer.opacity, "1");

    await button.evaluate(element => { element.dataset.tooltip = "Copied"; });
    await page.waitForFunction(() => document.querySelector("#icon-tooltip-layer")?.textContent === "Copied");
    await page.mouse.move(600, 500);
    await page.waitForFunction(() => !document.querySelector("#icon-tooltip-layer")?.matches(":popover-open"));

    await button.focus();
    await page.waitForFunction(() => document.querySelector("#icon-tooltip-layer")?.matches(":popover-open"));
    assert.equal(await tooltip.textContent(), "Copied");
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => !document.querySelector("#icon-tooltip-layer")?.matches(":popover-open"));

    await page.locator("[data-dialog]").evaluate(dialog => dialog.showModal());
    await page.locator("[data-dialog] .icon-tooltip").hover();
    await page.waitForFunction(() => document.querySelector("#icon-tooltip-layer")?.matches(":popover-open"));
    assert.equal(await tooltip.textContent(), "Copy error details");
    assert.equal(await tooltip.evaluate(element => element.matches(":popover-open")), true, "dialog copy tooltip must remain above the modal");
    assert.deepEqual(failures, [], `browser errors: ${failures.join("\n")}`);
    process.stdout.write("Icon tooltip layer contract passed.\n");
  } finally {
    await browser.close();
  }
})().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
