const assert = require('node:assert/strict');

module.exports = async function verifyTabSwitch(page) {
  const wasEditing = await page.locator('[data-workbench]').getAttribute('data-mode') === 'edit';
  const tabs = page.locator('.wb-tabs button');
  const original = await page.locator('.wb-tabs [aria-pressed=true]').getAttribute('data-space-id');
  const ids = await tabs.evaluateAll(nodes => nodes.map(node => node.dataset.spaceId));
  const other = ids.find(id => id !== original);
  assert(other, 'tab switching needs two spaces');
  const find = id => page.locator('.wb-tabs [data-space-id="' + id + '"]');
  async function clickOnce(id) {
    const target = find(id);
    await target.scrollIntoViewIfNeeded();
    const node = await target.elementHandle();
    const box = await target.boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    // Let focusout synchronization run between pointerdown and click.
    await page.waitForTimeout(100);
    const retained = await node.evaluate(element => element.isConnected);
    await page.mouse.up();
    assert(retained, 'focusout must preserve the pressed tab node');
    assert.equal(await page.locator('.wb-tabs [aria-pressed=true]').getAttribute('data-space-id'), id, 'one click must switch spaces');
    await node.dispose();
  }
  await find(original).focus();
  await clickOnce(other);
  await clickOnce(original);
  await page.getByRole('button', {name: /^(编辑|Edit)$/, exact: true}).click();
  const note = page.locator('.wb-note textarea').first();
  await note.focus();
  await clickOnce(other);
  await clickOnce(original);
  await find(other).focus();
  await page.keyboard.press('Enter');
  assert.equal(await page.locator('.wb-tabs [aria-pressed=true]').getAttribute('data-space-id'), other);
  await clickOnce(original);
  if (!wasEditing) await page.getByRole('button', {name: /^(查看|View)$/, exact: true}).click();
};
