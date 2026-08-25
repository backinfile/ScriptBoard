const assert = require("node:assert/strict");
const path = require("node:path");
const { chromium } = require("playwright");

function pageDocument(enabled, includeNavigation = true) {
  return `<!doctype html><html><body data-app-shell>
    ${includeNavigation ? `<aside class="app-sidebar"><nav class="sidebar-nav"><section class="nav-group"><h2>配置</h2><a href="/config/custom-tabs">自定义页签</a></section>${enabled ? '<section class="nav-group"><h2>外部</h2><a href="/defined/tabs/example" data-native>本地文档</a></section>' : ""}</nav></aside>` : ""}
    <main class="workspace custom-tabs-workspace" data-custom-tabs-page>
      <header class="page-heading"><button type="button" data-dashboard-open-drawer="custom-tab-create">新建页签</button></header>
      <form method="post" action="/config/custom-tabs/example/${enabled ? "toggle-off" : "toggle-on"}" data-async data-async-refresh="[data-custom-tabs-page]"><input type="hidden" name="enabled" value="${enabled ? "false" : "true"}"><button type="submit">${enabled ? "关闭" : "开启"}</button></form>
      <p data-tab-state>${enabled ? "已开启" : "未开启"}</p>
      <details class="custom-dashboard-drawer-host" data-dashboard-drawer data-dashboard-drawer-id="custom-tab-create"><summary class="sr-only">新建页签</summary><div class="custom-dashboard-drawer-layer"><button class="custom-dashboard-drawer-scrim" type="button" data-dashboard-drawer-close></button><aside class="overview-drawer custom-tab-drawer"><header class="overview-drawer__head"><div><p>自定义页签</p><h2>新建页签</h2></div><button type="button" data-dashboard-drawer-close>关闭</button></header><div class="overview-drawer__body"><form class="overview-drawer__form"><p class="overview-drawer__description">配置说明</p><label>页签名称<input></label><footer><button>取消</button><button>创建页签</button></footer></form></div></aside></div></details>
    </main>
    <div class="overview-drawer-host is-open" data-reference-machine-drawer aria-hidden="false" style="visibility:hidden;pointer-events:none"><button class="overview-drawer-scrim"></button><aside class="overview-drawer"><header class="overview-drawer__head"><div><p>机器</p><h2>添加机器</h2></div><button>关闭</button></header><div class="overview-drawer__body"><form class="overview-drawer__form"><p class="overview-drawer__description">添加说明</p><label>名称<input></label><footer><button>取消</button><button>连接机器</button></footer></form></div></aside></div>
  </body></html>`;
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } });
  let enabled = false;
  await page.route(/^http:\/\/custom-tabs\.test\/config\/custom-tabs$/, (route) => route.fulfill({ contentType: "text/html; charset=utf-8", body: pageDocument(enabled) }));
  await page.route("http://custom-tabs.test/config/custom-tabs/example/toggle-on", (route) => { enabled = true; return route.fulfill({ contentType: "text/html; charset=utf-8", body: pageDocument(true, false) }); });
  await page.route("http://custom-tabs.test/config/custom-tabs/example/toggle-off", (route) => { enabled = false; return route.fulfill({ contentType: "text/html; charset=utf-8", body: pageDocument(false, false) }); });
  await page.goto("http://custom-tabs.test/config/custom-tabs");
  const repository = path.resolve(__dirname, "../..");
  await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
  await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });
  await page.waitForTimeout(300);

  await page.locator('[data-dashboard-open-drawer="custom-tab-create"]').click();
  const drawerStyle = await page.evaluate(() => {
    const metrics = (panel, head, form) => {
      const panelStyle = getComputedStyle(panel);
      const headStyle = getComputedStyle(head);
      const titleStyle = getComputedStyle(head.querySelector("h2"));
      const formStyle = getComputedStyle(form);
      return { width: panelStyle.width, minWidth: panelStyle.minWidth, headPadding: headStyle.padding, titleSize: titleStyle.fontSize, formPadding: formStyle.padding, formGap: formStyle.gap };
    };
    const target = document.querySelector(".custom-tab-drawer");
    const reference = document.querySelector("[data-reference-machine-drawer] .overview-drawer");
    return {
      target: metrics(target, target.querySelector("header"), target.querySelector("form")),
      reference: metrics(reference, reference.querySelector("header"), reference.querySelector("form")),
      usesReferenceStructure: target.querySelector("header")?.classList.contains("overview-drawer__head") && !!target.querySelector(".overview-drawer__body > .overview-drawer__form"),
    };
  });
  assert.equal(drawerStyle.usesReferenceStructure, true, "custom-tab drawers must use the Add machine drawer header, body, and form structure");
  assert.deepEqual(drawerStyle.target, drawerStyle.reference, `custom-tab drawer format must match Add machine: ${JSON.stringify(drawerStyle)}`);
  const drawerBounds = await page.locator(".custom-dashboard-drawer-layer").evaluate((element) => {
    const bounds = element.getBoundingClientRect();
    return { top: bounds.top, left: bounds.left, width: bounds.width, height: bounds.height, layoutWidth: document.documentElement.clientWidth, viewportHeight: innerHeight };
  });
  assert.ok(drawerBounds.top === 0 && drawerBounds.left === 0 && drawerBounds.height === drawerBounds.viewportHeight && drawerBounds.width >= drawerBounds.layoutWidth - 20, `custom-tab drawers must cover the viewport instead of inheriting the animated workspace child as a containing block: ${JSON.stringify(drawerBounds)}`);
  await page.goto("http://custom-tabs.test/config/custom-tabs");
  await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
  await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });

  await page.locator('form[action$="toggle-on"] button').click();
  await page.locator("[data-tab-state]").getByText("已开启").waitFor();
  await page.locator(".sidebar-nav").getByRole("heading", { name: "外部" }).waitFor();
  assert.equal(await page.locator('.sidebar-nav a[href="/defined/tabs/example"]').getAttribute("data-native"), "", "enabling a custom tab must immediately add its native external navigation item");
  await page.locator('form[action$="toggle-off"] button').click();
  await page.locator("[data-tab-state]").getByText("未开启").waitFor();
  assert.equal(await page.locator('.sidebar-nav a[href="/defined/tabs/example"]').count(), 0, "disabling a custom tab must immediately remove its external navigation item");

  await page.setViewportSize({ width: 700, height: 800 });
  await page.goto("http://custom-tabs.test/config/custom-tabs");
  await page.addStyleTag({ path: path.join(repository, "internal/web/ui/assets/app.css") });
  await page.addScriptTag({ path: path.join(repository, "internal/web/ui/assets/app.js") });
  await page.locator('[data-dashboard-open-drawer="custom-tab-create"]').click();
  const mobileWidths = await page.evaluate(() => ({
    target: getComputedStyle(document.querySelector(".custom-tab-drawer")).width,
    reference: getComputedStyle(document.querySelector("[data-reference-machine-drawer] .overview-drawer")).width,
  }));
  assert.equal(mobileWidths.target, mobileWidths.reference, "custom-tab and Add machine drawers must use the same mobile width");

  await browser.close();
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
