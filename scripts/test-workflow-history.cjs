// Uses the retained local test deployment at port 18910. Configure Chrome, Playwright and login environment variables as in test-workflow-drawers.cjs.

const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
(async () => {
  const b = await chromium.launch({
    executablePath: process.env.CHROME_EXECUTABLE,
    headless: true,
  });
  try {
    const p = await b.newPage({ viewport: { width: 1500, height: 950 } });
    await p.goto("http://127.0.0.1:18910/login");
    await p.fill("[name=username]", process.env.WORKFLOW_TEST_USER || "admin");
    await p.fill("[name=password]", process.env.WORKFLOW_TEST_PASSWORD);
    await p.locator("button[type=submit]").first().click();
    await p.waitForTimeout(400);
    await p.goto("http://127.0.0.1:18910/workflow");
    await p.waitForSelector(".wf-entry");
    await p.waitForTimeout(600);
    let nav = 0;
    p.on("request", (r) => {
      if (r.isNavigationRequest() && r.frame() === p.mainFrame()) nav++;
    });
    await p.screenshot({ path: ".scratch/workflow-deploy/entry-quickrun.png" });
    await p.click("[data-wf-tab=history]");
    await p.locator(".wf-run summary").first().click();
    await p.waitForTimeout(500);
    await p.screenshot({
      path: ".scratch/workflow-deploy/history-actions.png",
    });
    await p.locator("[data-action=history-step]").first().click();
    if (
      (await p
        .locator(".wf-run[open] .wf-job-list [aria-pressed=true]")
        .count()) !== 1
    )
      throw Error("step selection");
    await p.click("[data-wf-tab=entries]");
    await p.locator(".action-menu summary").first().click();
    await p.waitForTimeout(3000);
    if (
      !(await p
        .locator(".action-menu[open]>div")
        .first()
        .evaluate((e) => e.matches(":popover-open")))
    )
      throw Error("poll closed menu");
    await p.keyboard.press("Escape");
    await p.locator(".action-menu summary").first().click();
    await p.locator("[data-action=entry-edit]").first().click();
    await p.waitForTimeout(200);
    await p.mouse.click(280, 180);
    await p.waitForFunction(
      () => !document.querySelector("[data-wf-drawer]").open,
    );
    await p.setViewportSize({ width: 390, height: 844 });
    await p.click("[data-wf-tab=history]");
    await p.waitForTimeout(500);
    if (
      await p.evaluate(() => document.documentElement.scrollWidth > innerWidth)
    )
      throw Error("overflow");
    await p.screenshot({ path: ".scratch/workflow-deploy/history-mobile.png" });
    if (nav) throw Error("navigation");
    console.log("HISTORY MENU DRAWER PASS");
  } finally {
    await b.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
