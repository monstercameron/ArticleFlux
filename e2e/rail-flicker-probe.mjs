// Probe 7. Clean room. No clicking anything — probe 5/6 were contaminated
// because a click's state write does not repaint until some later event does
// (TODO H11), so the click landed in the middle of the measurement window.
//
// Boot, settle, scroll. Record what changes in the rail and whether the data
// behind it changed at all.
import { chromium } from '@playwright/test';

const BASE = process.argv[2] || 'http://127.0.0.1:9001';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
await page.goto(BASE);

const pw = page.locator('input[type="password"]');
if (await pw.count()) {
  await page.locator('input').first().fill('cam');
  await pw.fill('articleflux');
  await page.getByRole('button', { name: /sign in/i }).click();
}
await page.locator('.shell').waitFor({ timeout: 90_000 });
await page.locator('.item-row').first().waitFor({ timeout: 60_000 });
await page.waitForLoadState('networkidle').catch(() => {});
await page.waitForTimeout(5000);

const before = await page.evaluate(() => ({
  bands: [...document.querySelectorAll('.rail-scroll .rail-band')]
    .map(b => `${b.querySelector('.rail-band-label')?.textContent}=${b.dataset.open}`),
  slots: document.querySelectorAll('.rail-scroll .feed-slot').length,
  kids: document.querySelector('.rail-scroll').children.length,
}));

await page.evaluate(() => {
  window.__p = { anim: 0, animOn: {}, add: {}, rem: {}, badges: new Set(), kidCounts: new Set(), events: [] };
  const desc = (n) => n.nodeType !== 1 ? '#text'
    : `${n.tagName.toLowerCase()}.${String(n.className).replace(/\s+/g, '_') || '-'}`;

  document.addEventListener('animationstart', (e) => {
    if (!e.target.closest?.('.rail-scroll')) return;
    window.__p.anim++;
    const c = desc(e.target);
    window.__p.animOn[c] = (window.__p.animOn[c] || 0) + 1;
  }, true);

  const mo = new MutationObserver((recs) => {
    for (const r of recs) {
      if (!r.target.closest?.('.rail-scroll')) continue;
      for (const n of r.removedNodes) { const k = desc(n); window.__p.rem[k] = (window.__p.rem[k] || 0) + 1; }
      for (const n of r.addedNodes)   { const k = desc(n); window.__p.add[k] = (window.__p.add[k] || 0) + 1; }
      if (window.__p.events.length < 24)
        window.__p.events.push(`${desc(r.target)}: -${r.removedNodes.length} +${r.addedNodes.length}`);
    }
  });
  mo.observe(document.documentElement, { childList: true, subtree: true });

  const t = setInterval(() => {
    window.__p.kidCounts.add(document.querySelector('.rail-scroll').children.length);
    window.__p.badges.add([...document.querySelectorAll('.rail-scroll .feed-count')]
      .map(e => e.textContent).join(','));
  }, 16);

  window.__pStop = () => {
    mo.disconnect(); clearInterval(t);
    window.__p.badges = [...window.__p.badges];
    window.__p.kidCounts = [...window.__p.kidCounts];
    return window.__p;
  };
});

const list = page.locator('.list-scroll').first();
await list.hover();
for (let i = 0; i < 60; i++) { await page.mouse.wheel(0, 300); await page.waitForTimeout(35); }
await page.waitForTimeout(400);

const p = await page.evaluate(() => window.__pStop());
const after = await page.evaluate(() => ({
  bands: [...document.querySelectorAll('.rail-scroll .rail-band')]
    .map(b => `${b.querySelector('.rail-band-label')?.textContent}=${b.dataset.open}`),
  slots: document.querySelectorAll('.rail-scroll .feed-slot').length,
  kids: document.querySelector('.rail-scroll').children.length,
}));

console.log('rail BEFORE :', JSON.stringify(before));
console.log('rail AFTER  :', JSON.stringify(after));
console.log('rail child-count values seen during scroll:', JSON.stringify(p.kidCounts));
console.log('distinct unread-badge strings            :', p.badges.length,
  p.badges.length === 1 ? '<-- DATA NEVER CHANGED: the rail re-rendered for nothing' : '<-- counts moved');
console.log('animation starts in rail :', p.anim);
console.log('   on                    :', JSON.stringify(p.animOn));
console.log('nodes ADDED   :', JSON.stringify(p.add));
console.log('nodes REMOVED :', JSON.stringify(p.rem));
console.log('first records :\n  ' + p.events.join('\n  '));

await browser.close();
