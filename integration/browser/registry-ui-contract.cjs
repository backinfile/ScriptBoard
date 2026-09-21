const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require('playwright');
const assets = path.resolve(__dirname, '../../internal/web/ui/assets');
const css = ['app.css', 'registries.css', 'resource-workspace.css'].map(f => fs.readFileSync(path.join(assets, f), 'utf8')).join('\n');
const image = name => `<div data-registry-image data-registry-path="${name}"><strong>${name}</strong><button data-registry-tag>latest</button></div>`;
const trees = () => ['one', 'two'].map(id => `<div data-registry-card-tree="${id}">${['team/api/web','team/api/worker','team/db','standalone'].map(image).join('')}</div>`).join('');
(async () => {
  const browser = await chromium.launch({headless:true});
  try {
    const page = await browser.newPage();
    await page.route('http://registry.test/**', r => r.fulfill({contentType:'text/html',body:`<!doctype html><html><body><main class="workspace registry-page resource-page"><header class="page-heading resource-page-heading"><h1>Registry</h1></header><div class="registry-workspace resource-workspace"><aside class="resource-connection-rail"><nav class="registry-connections resource-connection-list"><a>Test connection</a></nav></aside><section class="registry-content resource-detail"><header class="registry-detail-heading"><h2>Selected connection</h2></header><section class="registry-records"><p class="registry-empty">No history</p></section></section></div></main>${trees()}</body></html>`}));
    await page.goto('http://registry.test/');
    await page.addStyleTag({content:css});
    for(const width of [1840,1440,1024,768,390]) {
      await page.setViewportSize({width,height:764});
      const box = await page.evaluate(() => {
        const rail = document.querySelector('.resource-connection-rail').getBoundingClientRect();
        const detail = document.querySelector('.resource-detail').getBoundingClientRect();
        return {railTop:rail.y,railRight:rail.right,railBottom:rail.bottom,detailTop:detail.y,detailLeft:detail.x,right:detail.right};
      });
      assert(box.right <= width + 1, JSON.stringify(box));
      if(width > 1000) { assert(Math.abs(box.detailTop-box.railTop)<2); assert(box.detailLeft>=box.railRight-1); }
      else assert(box.detailTop-box.railBottom<2);
    }
    const initialize = () => page.addScriptTag({path:path.join(assets,'registry-card-tree.js')});
    await initialize();
    const one = page.locator('[data-registry-card-tree="one"]');
    const parent = one.locator('[data-registry-prefix="team/"]');
    const child = one.locator('[data-registry-prefix="team/api/"]');
    assert.equal(await one.locator('details').count(),2);
    assert.equal(await parent.locator(':scope>summary small').textContent(),'3');
    await child.locator(':scope>summary').click();
    await page.waitForFunction(() => JSON.parse(localStorage.getItem('scriptboard.dashboard.registry.one.collapsed')).includes('team/api/'));
    await parent.locator(':scope>summary').click();
    await page.waitForFunction(() => JSON.parse(localStorage.getItem('scriptboard.dashboard.registry.one.collapsed')).includes('team/'));
    assert.notEqual(await page.locator('[data-registry-card-tree="two"] [data-registry-prefix="team/"]').getAttribute('open'),null);
    await page.reload(); await initialize();
    assert.equal(await parent.getAttribute('open'),null);
    await parent.locator(':scope>summary').click();
    assert.equal(await child.getAttribute('open'),null);
    await child.locator(':scope>summary').click();
    await one.locator('[data-registry-tag]').first().click();
    assert.equal(await one.locator('[data-registry-image]').count(),4);
    await page.evaluate(() => {
      localStorage.setItem('scriptboard.dashboard.registry.two.collapsed','malformed');
      const tree = document.querySelector('[data-registry-card-tree="two"]');
      tree.outerHTML = '<div data-registry-card-tree="two"><div data-registry-image data-registry-path="fresh/image">fresh/image</div></div>';
    });
    await page.locator('[data-registry-prefix="fresh/"]').waitFor();
    assert.notEqual(await page.locator('[data-registry-prefix="fresh/"]').getAttribute('open'),null);
    await page.addInitScript(() => {Storage.prototype.getItem = () => {throw Error('blocked')};Storage.prototype.setItem = () => {throw Error('blocked')};});
    await page.reload(); await initialize(); await parent.locator(':scope>summary').click();
    assert.equal(await parent.getAttribute('open'),null);
    console.log('PASS Registry: database workspace alignment, empty history, viewport bounds, nested card prefixes, local persistence, card isolation, refreshed DOM, malformed and blocked storage.');
  } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
