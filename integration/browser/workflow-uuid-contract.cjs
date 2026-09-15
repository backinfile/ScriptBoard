const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const origin of ['http://workflow.test', 'https://workflow.test']) {
      const page = await browser.newPage();
      const root = path.resolve(__dirname, '../..');
      const template = fs.readFileSync(path.join(root, 'internal/web/ui/templates/workflow.html'), 'utf8')
        .replaceAll('{{.CanManage}}', 'true').replaceAll('{{.CanExecute}}', 'true');
      let saved;
      await page.route(origin + '/**', route => {
        const url = new URL(route.request().url());
        if (url.pathname === '/workflow/state') return route.fulfill({ json: { draft: saved ? [saved] : [], published: [], custom: [], entry: [], runs: [] } });
        if (url.pathname === '/workflow/save') {
          saved = { ...route.request().postDataJSON(), id: 'test-workflow', revision: 1 };
          return route.fulfill({ json: saved });
        }
        if (url.pathname === '/workflow') return route.fulfill({ contentType: 'text/html', body: template });
        if (['/assets/workflow.js', '/assets/workflow.css'].includes(url.pathname)) return route.fulfill({ path: path.join(root, 'internal/web/ui', url.pathname) });
        return route.fulfill({ body: '' });
      });
      await page.goto(origin + '/workflow?tab=editor');
      assert.equal(await page.evaluate(() => typeof crypto.randomUUID), origin.startsWith('https') ? 'function' : 'undefined');
      await page.locator('[data-action="flow-new"]').evaluate(el => el.click());
      const toast = await page.locator('[data-wf-toast]').textContent();
      assert.ok(!toast.includes('randomUUID'), toast);
      await page.locator('[data-action="flow-save"]').click();
      await page.waitForFunction(() => !document.querySelector('[data-action="flow-save"]')?.disabled);
      assert.ok(saved, 'new workflow can be saved');
      const ids = saved.graph.nodes.map(node => node.id);
      assert.equal(ids.length, 2);
      assert.equal(new Set(ids).size, 2);
      assert.ok(ids.every(id => /^[0-9a-f-]{32,36}$/.test(id)));
      await page.reload();
      await page.locator('[data-flow-select]').selectOption(saved.id);
      for (const id of ids) await page.locator('[data-node="' + id + '"]').waitFor();
      console.log('PASS workflow create/save/reload: ' + origin);
      await page.close();
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
