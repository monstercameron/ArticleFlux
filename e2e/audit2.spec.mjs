import { test, expect, boot, openNote, openRail, openRow, openStream, openSettings, isPhone, storeFakeKey } from './fixtures.mjs';

/**
 * The second UX audit's client-side fixes (G3-G6, G10, G11, G14, G15, G19-G24),
 * each driven the way a person would, in a real browser.
 *
 * One file because what they share is the reason they exist: every one is a thing
 * that was on screen and wrong while every Go test stayed green — focus that fell
 * to <body>, a Tab order, a dialog that did nothing, a pixel size. Markup-level
 * claims live in client/view/audit2_test.go; this is what only a browser can say.
 *
 * Pointer parked at (900, 400) before anything hover-dependent: Chromium on Linux
 * starts at (0, 0), which is over the rail.
 */

const desktopOnly = (testInfo, why) => test.skip(testInfo.project.name !== 'desktop', why);
const mobileOnly = (testInfo, why) => test.skip(testInfo.project.name !== 'mobile', why);

/** tagFirstArticle files the first article's feed under name and waits for the server. */
async function tagFirstArticle(page, name) {
  await openRow(page, 0);
  await openNote(page);
  const panel = page.locator('.article-note').first();
  await panel.locator('[data-role="tag"]').first().fill(name);
  await panel.locator('[data-action="add-tag"]').first().click();
  await expect(panel.locator('.tag-chip-pending')).toHaveCount(0, { timeout: 45_000 });
  await expect(panel.locator('.tag-chip', { hasText: name })).toBeVisible({ timeout: 45_000 });
  return panel;
}

/** openTagSettings presses the gear on a tag's rail row. */
async function openTagSettings(page, name) {
  const rail = await openRail(page);
  const slot = rail.locator('.feed-slot')
    .filter({ has: page.locator('[data-tag-id]') })
    .filter({ hasText: name }).first();
  await slot.hover();
  await slot.locator('[data-action="tag-settings"]').click({ force: true });
  const panel = page.locator('[aria-label="Tag settings"]');
  await expect(panel).toBeVisible({ timeout: 30_000 });
  return panel;
}

/** newFolderButton is the + on the rail's Categories band. */
async function newFolderButton(page) {
  const rail = await openRail(page);
  const btn = rail.locator('[data-action="category-new"]');
  await rail.locator('.rail-band').filter({ has: page.locator('[data-action="category-new"]') }).hover();
  return btn;
}

const folderDialog = (page) => page.locator('.pal-scrim:has([aria-label="Folder"])');
const folderRows = (page) => page.locator('.pane-rail .cat-row');

/** The element that has focus, described the way a test can compare. */
const focused = (page) => page.evaluate(() => {
  const a = document.activeElement;
  if (!a || a === document.body) return { body: true };
  return { tag: a.tagName, cls: a.className.toString(), action: a.dataset.action || '',
    role: a.dataset.role || '', stop: a.getAttribute('tabindex') };
});

test.describe('tag chips (G3, G4)', () => {
  test('a chip says the tag, and its two halves work from the keyboard at a usable size', async ({ page }) => {
    await boot(page);
    const panel = await tagFirstArticle(page, 'rust');

    // Renamed for the sidebar: the chip must not follow it.
    const dlg = await openTagSettings(page, 'rust');
    await dlg.locator('[data-role="tag-label"]').fill('Systems ⚙ stuff');
    await dlg.locator('[data-action="ts-rename"]').click();
    await expect(dlg.locator('[data-role="tag-label"]')).toHaveValue('Systems ⚙ stuff', { timeout: 45_000 });
    await dlg.locator('button[data-action="tag-settings-close"]').click();
    await expect(dlg).toBeHidden();
    if (await isPhone(page)) await openRow(page, 0);

    const chip = panel.locator('.tag-chip').first();
    await expect(chip.locator('.tag-open')).toHaveText('rust');
    await expect(chip.locator('.tag-x')).toHaveAttribute('aria-label', 'Remove tag rust');

    // Two real buttons, each its own Tab stop, and the remove target is not the
    // 7x13 glyph it was.
    expect(await chip.locator('button').count()).toBe(2);
    const x = await chip.locator('.tag-x').boundingBox();
    expect(x.width).toBeGreaterThanOrEqual(24);
    expect(x.height).toBeGreaterThanOrEqual(24);

    // Enter on the label opens the tag's stream: it did nothing, because the
    // data-tag-id sat on a child span.
    await chip.locator('.tag-open').focus();
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => location.pathname)).toMatch(/^\/tag\//);

    // And the remove control is reachable and works from the keyboard.
    if (await isPhone(page)) await openRow(page, 0);
    const again = page.locator('.article-note').first().locator('.tag-chip').first();
    await again.locator('.tag-x').focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('.article-note').first().locator('.tag-chip')).toHaveCount(0, { timeout: 45_000 });
  });
});

test.describe('tag settings (G5, G14, G19)', () => {
  test('the rename field states and enforces its limit', async ({ page }) => {
    await boot(page);
    await tagFirstArticle(page, 'rust');
    const dlg = await openTagSettings(page, 'rust');

    const field = dlg.locator('[data-role="tag-label"]');
    await expect(field).toHaveAttribute('maxlength', '48');
    await expect(dlg).toContainText('Up to 48 characters');

    // The refusal itself (the server answering a label past its cap) is proven as
    // markup in client/view/audit2_test.go; it is not driven from here because the
    // harness server accepted a 120-character label when this was written — the
    // server-side cap is the other half of G7 and is asserted in Go.
  });

  test('closing the panel after a rename puts focus back on the tag’s gear', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'a phone has no keyboard to lose focus from');
    await boot(page);
    await tagFirstArticle(page, 'rust');
    const dlg = await openTagSettings(page, 'rust');
    await dlg.locator('[data-role="tag-label"]').fill('Zeta systems');
    await dlg.locator('[data-action="ts-rename"]').click();
    await expect(dlg.locator('[data-role="tag-label"]')).toHaveValue('Zeta systems', { timeout: 45_000 });
    await page.keyboard.press('Escape');
    await expect(dlg).toBeHidden();
    await expect.poll(async () => (await focused(page)).role ?? '').toMatch(/^tag-gear-/);
  });

  test('the tag field on an article stops at the cap', async ({ page }) => {
    await boot(page);
    await openRow(page, 0);
    await openNote(page);
    await expect(page.locator('.article-note').first().locator('[data-role="tag"]'))
      .toHaveAttribute('maxlength', '48');
  });
});

test.describe('the folder editor (G6, G14, G15, G19, G25)', () => {
  test('+ NEW makes nothing until a name is saved, and every way out leaves no stray folder', async ({ page }) => {
    await boot(page);
    const dlg = folderDialog(page);
    await expect(folderRows(page)).toHaveCount(0);

    // Cancel, Close and Escape: each used to leave a real "New folder".
    for (const out of ['cancel', 'close', 'escape']) {
      await (await newFolderButton(page)).click();
      await expect(dlg).toHaveAttribute('data-open', 'true');
      await expect(dlg).toContainText('New folder');
      await expect(dlg.locator('[data-action="category-delete"]')).toHaveCount(0);
      if (out === 'cancel') await dlg.locator('.af-foot [data-action="category-close"]').click();
      if (out === 'close') await dlg.locator('button.af-close, .af-head [data-action="category-close"]').first().click();
      if (out === 'escape') await page.keyboard.press('Escape');
      await expect(dlg).toHaveAttribute('data-open', 'false');
      await expect(folderRows(page)).toHaveCount(0);
      await expect(page.locator('.pane-rail')).not.toContainText('New folder');
    }

    // Focus came back to the + each time (it fell to <body>).
    // Waited for, not sampled: focus is handed back after the dialog has closed
    // and the row list has settled, a frame or several later, and a single look
    // the instant the dialog reports closed caught it in between on CI's runners
    // about one run in three. If it never comes back, this still fails.
    if (!(await isPhone(page))) {
      await expect.poll(async () => (await focused(page)).action, { timeout: 5_000 }).toBe('category-new');
    }
  });

  test('an empty name is refused in the folder’s own words, on the field, and a name creates it', async ({ page }) => {
    await boot(page);
    const dlg = folderDialog(page);
    await (await newFolderButton(page)).click();
    const field = dlg.locator('[data-role="category-name"]');
    await expect(field).toHaveAttribute('maxlength', '48');
    await expect(dlg).toContainText('Up to 48 characters.');

    await dlg.locator('[data-action="category-save"]').click();
    await expect(dlg.locator('[role="alert"]')).toHaveText('A folder needs a name.');
    await expect(field).toHaveAttribute('aria-invalid', 'true');
    await expect(dlg).toHaveAttribute('data-open', 'true');

    await field.fill('Audit folder');
    await field.press('Enter');
    await expect(dlg).toHaveAttribute('data-open', 'false');
    await expect(folderRows(page).filter({ hasText: 'Audit folder' })).toBeVisible({ timeout: 30_000 });
    // Said in full where the rail would cut it.
    await expect(folderRows(page).filter({ hasText: 'Audit folder' }).locator('.feed-name'))
      .toHaveAttribute('title', 'Audit folder');
    // Waited for, not sampled: focus is handed back after the dialog has closed
    // and the row list has settled, a frame or several later, and a single look
    // the instant the dialog reports closed caught it in between on CI's runners
    // about one run in three. If it never comes back, this still fails.
    if (!(await isPhone(page))) {
      await expect.poll(async () => (await focused(page)).action, { timeout: 5_000 }).toBe('category-new');
    }
  });

  test('an armed delete answers Escape and a press elsewhere with "no" and keeps the dialog', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'Escape and arrow keys are a keyboard claim');
    await boot(page);
    const dlg = folderDialog(page);
    await (await newFolderButton(page)).click();
    await dlg.locator('[data-role="category-name"]').fill('Doomed');
    await dlg.locator('[data-action="category-save"]').click();
    const row = folderRows(page).filter({ hasText: 'Doomed' });
    await expect(row).toBeVisible({ timeout: 30_000 });

    // Open it from its gear.
    await row.hover();
    await page.locator('.pane-rail .cat-gear').first().click({ force: true });
    await expect(dlg).toHaveAttribute('data-open', 'true');
    const del = dlg.locator('[data-action="category-delete"]');
    const armed = dlg.locator('[data-armed="true"]');

    // Escape on the armed button: disarm, and stay.
    await del.click();
    await expect(armed).toHaveCount(1);
    await page.keyboard.press('Escape');
    await expect(armed).toHaveCount(0);
    await expect(dlg).toHaveAttribute('data-open', 'true');

    // A click into the name field: it used to stay armed.
    await dlg.locator('[data-action="category-delete"]').click();
    await expect(armed).toHaveCount(1);
    await dlg.locator('[data-role="category-name"]').click();
    await expect(armed).toHaveCount(0);

    // Escape from inside the field with it armed: also "no" first.
    await dlg.locator('[data-action="category-delete"]').click();
    await expect(armed).toHaveCount(1);
    await dlg.locator('[data-role="category-name"]').focus();
    await page.keyboard.press('Escape');
    await expect(armed).toHaveCount(0);
    await expect(dlg).toHaveAttribute('data-open', 'true');

    // And a second Escape closes it, as before.
    await page.keyboard.press('Escape');
    await expect(dlg).toHaveAttribute('data-open', 'false');
  });
});

test.describe('the sidebar is one Tab stop (G10)', () => {
  test('Tab visits the pane once; the arrows move inside it, gears and all', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'a keyboard claim');
    await boot(page);
    await page.mouse.move(900, 400);

    const stops = await page.evaluate(() =>
      [...document.querySelectorAll('.pane-rail button, .pane-rail input, .pane-rail a[href]')]
        .filter((e) => e.tabIndex >= 0 && !e.disabled).map((e) => e.className.toString().split(' ')[0]));
    // The one stop, plus the foot's own two controls: Add a feed and the fold.
    expect(stops.length, `Tab stops in the rail: ${stops.join(', ')}`).toBeLessThanOrEqual(3);
    expect(stops).toContain('feed-row');
    // The stop is where the reader is.
    expect(await page.evaluate(() =>
      document.querySelector('.pane-rail .feed-row[tabindex="0"]')?.getAttribute('aria-current'))).toBe('true');

    // Down, then across to the gear, then down from the gear lands on the next
    // ROW, then left returns to a row.
    const rowWithGear = page.locator('.pane-rail .feed-slot:has(.feed-gear) .feed-row').first();
    await rowWithGear.focus();
    await page.keyboard.press('ArrowRight');
    expect((await focused(page)).cls).toContain('feed-gear');
    await page.keyboard.press('ArrowDown');
    expect((await focused(page)).cls).toContain('feed-row');
    const second = await page.evaluate(() => document.activeElement.dataset.sourceId);
    expect(second).toBeTruthy();
    await page.keyboard.press('ArrowRight');
    await page.keyboard.press('ArrowLeft');
    expect((await focused(page)).cls).toContain('feed-row');

    // Tab from inside the pane leaves it for the next real control (the foot),
    // not for the next row.
    await page.keyboard.press('Tab');
    expect((await focused(page)).action).toBe('add-feed-open');
  });
});

test.describe('where focus goes (G14, G15)', () => {
  test('Escape in the search box lands in the list, not on <body>', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'a keyboard claim');
    await boot(page);
    await page.keyboard.press('/');
    await expect.poll(async () => (await focused(page)).role).toBe('search');
    await page.keyboard.press('Escape');
    await expect.poll(async () => (await focused(page)).cls ?? '').toContain('item-row');
  });

  test('"," moves focus onto the settings screen, so Tab starts there', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'a keyboard claim');
    await boot(page);
    await page.evaluate(() => document.activeElement?.blur());
    await page.keyboard.press(',');
    await expect(page.locator('.set-tabs')).toBeVisible();
    await expect.poll(async () => (await focused(page)).cls ?? '').toContain('pane-settings');
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => !!document.activeElement.closest('.pane-settings'))).toBe(true);
  });

  test('an armed Mark all read does not survive the shortcut sheet', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'a keyboard claim');
    await boot(page);
    await page.locator('[data-action="mark-all-arm"]').click();
    const confirm = page.locator('[data-role="mark-all-confirm"]');
    await expect(confirm).toBeVisible();
    await page.keyboard.press('Escape'); // out of the banner, keeps it armed? no: cancels it
    await page.locator('[data-action="mark-all-arm"]').click();
    await expect(confirm).toBeVisible();
    await page.evaluate(() => document.activeElement?.blur());
    await page.keyboard.press('?');
    await expect(page.locator('.help[role="dialog"]')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(confirm).toHaveCount(0);
  });
});

test.describe('a note is not lost to a reload or an outage (G11, G20)', () => {
  test('text typed just before a reload is kept', async ({ page }) => {
    await boot(page);
    await openRow(page, 0);
    const field = await openNote(page);
    const text = `typed late ${Date.now()}`;
    await field.fill(text);
    // Inside the 800ms debounce: nothing has been sent yet.
    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    // The outbox is written before the page goes and replayed when it is back.
    await expect.poll(() => page.evaluate(() => localStorage.getItem('articleflux.outbox.v1')),
      { timeout: 60_000 }).toBeFalsy();
    await openRow(page, 0);
    const again = await openNote(page);
    await expect(again).toHaveValue(text, { timeout: 30_000 });
  });

  test('offline the mark says Not saved in words, and ticks once the note lands', async ({ page, context }, testInfo) => {
    desktopOnly(testInfo, 'the outage choreography is the same on a phone; one project is enough');
    await boot(page);
    await openRow(page, 0);
    const field = await openNote(page);
    await context.setOffline(true);
    await expect(page.locator('.conn').first()).toHaveAttribute('data-state', /offline|down/, { timeout: 60_000 });
    await field.fill(`written offline ${Date.now()}`);
    const mark = page.locator('.article-note').first().locator('.note-sync');
    await expect(mark).toHaveAttribute('data-sync', 'queued', { timeout: 15_000 });
    await expect(mark).toContainText('Not saved');
    await expect(mark.locator('.sync-words')).toBeVisible();

    await context.setOffline(false);
    await expect(mark).toHaveAttribute('data-sync', 'saved', { timeout: 90_000 });
  });
});

test.describe('the welcome page (G21, G27)', () => {
  test('fits 320px, opens with its own heading, and does not offer to claim a claimed server', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 });
    await page.goto('/welcome');
    await expect(page.locator('h1')).toBeVisible();
    const m = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      first: document.querySelector('h1,h2,h3,h4,h5,h6')?.tagName,
      text: document.body.innerText,
    }));
    expect(m.scroll).toBeLessThanOrEqual(320);
    expect(m.first).toBe('H1');
    expect(m.text).not.toContain('Claim this server');
    expect(m.text).toContain('Open the app');
  });
});

test.describe('layout (G22)', () => {
  test('the now-playing bar is centred on the reading column, not on the window', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'the three-pane layout');
    await boot(page);
    await openRow(page, 0);
    // Not worth speaking an article for: put the bar's own element in.
    await page.evaluate(() => {
      const el = document.createElement('div');
      el.className = 'np'; el.id = 'np-probe';
      el.style.width = '640px'; el.style.height = '44px';
      document.querySelector('.shell').appendChild(el);
    });
    await page.waitForTimeout(500);
    const r = await page.evaluate(() => {
      const np = document.getElementById('np-probe').getBoundingClientRect();
      const art = document.querySelector('.pane-article').getBoundingClientRect();
      const list = document.querySelector('.pane-list').getBoundingClientRect();
      return { npL: np.left, npC: np.left + np.width / 2, artC: art.left + art.width / 2, listR: list.right };
    });
    expect(Math.abs(r.npC - r.artC)).toBeLessThanOrEqual(3);
    expect(r.npL).toBeGreaterThanOrEqual(r.listR);
  });
});

test.describe('touch targets (G23)', () => {
  test('the sidebar and the note controls are finger-sized on a phone', async ({ page }, testInfo) => {
    mobileOnly(testInfo, 'coarse pointers only');
    await boot(page);
    const rail = await openRail(page);
    const size = async (loc) => (await loc.first().boundingBox());
    const row = await size(rail.locator('.feed-row'));
    expect(row.height).toBeGreaterThanOrEqual(40);
    const gear = await size(rail.locator('.feed-gear'));
    expect(gear.width).toBeGreaterThanOrEqual(32);
    expect(gear.height).toBeGreaterThanOrEqual(32);
    const act = await size(rail.locator('.rail-band-act'));
    expect(act.width).toBeGreaterThanOrEqual(32);
    expect(act.height).toBeGreaterThanOrEqual(32);

    await openRow(page, 0);
    const toggle = await size(page.locator('.note-toggle'));
    expect(toggle.height).toBeGreaterThanOrEqual(32);
  });

  test('a desktop keeps its dense sidebar', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'fine pointers only');
    await boot(page);
    const row = await page.locator('.pane-rail .feed-row').first().boundingBox();
    expect(row.height).toBeLessThan(36);
  });
});

test.describe('Discover (G24)', () => {
  test('the disabled Refresh is not drawn in the accent', async ({ page }, testInfo) => {
    await boot(page);
    await storeFakeKey(page);
    await page.locator('[data-action="settings-tab"][data-value="discover"]').click();
    const refresh = page.locator('.discover-refresh');
    await expect(refresh).toBeDisabled();
    const same = await page.evaluate(() => {
      const r = document.querySelector('.discover-refresh');
      const probe = document.createElement('i');
      probe.style.borderTop = '1px solid var(--cc)';
      document.body.appendChild(probe);
      const accent = getComputedStyle(probe).borderTopColor;
      probe.remove();
      return getComputedStyle(r).borderTopColor === accent;
    });
    expect(same, 'a disabled control is outlined in the accent that means "press me"').toBe(false);
  });
});

test.describe('the voice notice (G24)', () => {
  test('a notice about the voice goes when the voice is stopped', async ({ page }, testInfo) => {
    desktopOnly(testInfo, 'keyboard');
    await boot(page);
    await openRow(page, 0);
    // Headless Chromium has no synthesiser, which is what makes the notice appear;
    // where there is one, there is no notice to withdraw.
    await page.evaluate(() => { Object.defineProperty(window, 'speechSynthesis', { value: undefined, configurable: true }); });
    await page.locator('[data-action="listen"]').first().click();
    const banner = page.locator('.pane-list .banner').first();
    const said = await banner.innerText({ timeout: 5_000 }).catch(() => '');
    test.skip(!/speech|voice/i.test(said), 'this browser took the listen without a notice');
    await page.keyboard.press('Escape');
    // The words, not the element: the list's banners live in slots that stay in
    // the DOM closed, so there are always two `.banner` elements and a count of
    // none was a test that could only pass by being skipped — which it was,
    // until Listen started raising its notice in this browser.
    await expect(banner).not.toContainText(/speech|voice/i);
  });
});

/**
 * outline reads the heading outline the way a screen reader's heading list gets
 * it: Chromium's own accessibility tree, over CDP. display:none stays out,
 * clip-path stays in, and an aria-level IS the level. Not Playwright's
 * ariaSnapshot, which derives a heading's level from its tag and so reports
 * <h1 aria-level="2"> as level 1 -- the opposite of what a browser exposes.
 */
async function outline(page) {
  const cdp = await page.context().newCDPSession(page);
  try {
    const { nodes } = await cdp.send('Accessibility.getFullAXTree');
    return nodes
      .filter((n) => !n.ignored && n.role?.value === 'heading')
      .map((n) => ({
        name: String(n.name?.value ?? '').trim(),
        level: Number(n.properties?.find((p) => p.name === 'level')?.value?.value),
      }));
  } finally {
    await cdp.detach();
  }
}

test.describe('the heading outline (G27)', () => {
  test('the reader has one level 1, first, and a stream of articles under it at level 2', async ({ page }) => {
    await boot(page);
    await openRow(page, 0);
    // A stream, not an article: the bug needed more than one title.
    await expect(page.locator('.article')).not.toHaveCount(1, { timeout: 30_000 });
    const titles = await page.locator('.article h1').evaluateAll((hs) => hs.map((h) => h.textContent.trim()));
    expect(titles.length).toBeGreaterThan(1);

    const heads = await outline(page);
    const ones = heads.filter((h) => h.level === 1);
    expect(ones, `level-1 headings in ${JSON.stringify(heads)}`).toHaveLength(1);
    expect(heads[0], 'the page heading comes first in the outline').toEqual({ name: expect.stringMatching(/^ArticleFlux — .+/), level: 1 });
    for (const t of titles) {
      expect(heads.find((h) => h.name === t), `article "${t}" in the outline`).toMatchObject({ level: 2 });
    }
    // The list's own title is the level 2 it always was. Desktop only: on a phone
    // the pane that is not showing (the list, behind the article) is out of the
    // accessibility tree altogether, which is right and is not this test's claim.
    if (!(await isPhone(page))) {
      const listTitle = (await page.locator('.list-title').innerText()).trim();
      expect(heads.find((h) => h.name === listTitle)).toMatchObject({ level: 2 });
      expect(heads[0].name).toBe(`ArticleFlux — ${listTitle}`);
    }
  });

  test('the page heading follows the scope', async ({ page }) => {
    await boot(page);
    await openStream(page, /^Read later/);
    const listTitle = (await page.locator('.list-title').innerText()).trim();
    await expect.poll(async () => (await outline(page))[0]?.name).toBe(`ArticleFlux — ${listTitle}`);
  });

  test('Settings has one level 1 of its own, and the reader\'s is gone', async ({ page }) => {
    await boot(page);
    await openRow(page, 0);
    const titles = await page.locator('.article h1').evaluateAll((hs) => hs.map((h) => h.textContent.trim()));
    await openSettings(page);
    const heads = await outline(page);
    const ones = heads.filter((h) => h.level === 1);
    expect(ones, JSON.stringify(heads)).toEqual([{ name: 'Settings', level: 1 }]);
    expect(heads.some((h) => /^ArticleFlux — /.test(h.name))).toBe(false);
    // Nothing from the article stream either: its pane is display:none behind Settings.
    expect(heads.some((h) => h.name === titles[0])).toBe(false);
  });
});
