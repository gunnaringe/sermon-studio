// The sermon in time (web/static/editor/spans.js), from sections and cuts alone: run in the browser, as the
// editor runs it, without a recording.
import { test, expect } from '@playwright/test';

// sermon makes sections and cuts from [start, end, pause] and works out what the editor shows.
async function sermon(page, sections, cuts) {
  await page.goto('style.css'); // any page of the studio: the module comes from it
  return page.evaluate(async ([sections, cuts]) => {
    const { skipped, activePauses, keptLength } = await import('./editor/spans.js');
    const span = ([start, end, pause = 0]) => ({ region: { start, end }, pause });
    const s = sections.map(span), c = cuts.map(span);
    return { skipped: skipped(s, c), pauses: activePauses(s, c), length: keptLength(s, c) };
  }, [sections, cuts]);
}

test('spans: one section, nothing left out', async ({ page }) => {
  expect(await sermon(page, [[10, 70]], [])).toEqual({ skipped: [], pauses: [], length: 60 });
});

test('spans: none, nothing to make', async ({ page }) => {
  expect(await sermon(page, [], [[1, 2]])).toEqual({ skipped: [{ start: 1, end: 2 }], pauses: [], length: 0 });
});

test('spans: the gap between sections and the cuts are left out; overlaps count once', async ({ page }) => {
  const got = await sermon(page, [[0, 30, 2], [40, 100]], [[20, 35], [50, 60, 1.5], [55, 65]]);
  expect(got.skipped).toEqual([{ start: 20, end: 35 }, { start: 30, end: 40 }, { start: 50, end: 60 }, { start: 55, end: 65 }]);
  expect(got.length).toBe(100 - 20 - 15); // 20–40 and 50–65 are out
  // The section's pause where it ends, and the cut's where it starts inside the sermon.
  expect(got.pauses).toEqual([{ at: 30, seconds: 2 }, { at: 50, seconds: 1.5 }]);
});

test('spans: a cut outside the sermon has no pause and takes nothing', async ({ page }) => {
  const got = await sermon(page, [[10, 20, 3]], [[0, 5, 1], [25, 30, 1]]);
  expect(got.pauses).toEqual([]); // the last section's pause is at the end: none
  expect(got.length).toBe(10);
});
