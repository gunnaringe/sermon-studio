// The sermon in time, from its sections and cuts (each with its region on the waveform: start and end): what is
// left out, where the pauses are, and how long it is. Nothing here touches the page.

// skipped are the parts left out between the first section's start and the
// last one's end: the gaps between sections and the cuts, in time order.
export function skipped(sections, cuts) {
  const list = cuts.map((c) => ({ start: c.region.start, end: c.region.end }));
  for (let i = 1; i < sections.length; i++) list.push({ start: sections[i - 1].region.end, end: sections[i].region.start });
  return list.sort((a, b) => a.start - b.start);
}

// activePauses are the pauses that take effect: where a section ends (but the last), and where a cut
// starts inside the sermon.
export function activePauses(sections, cuts) {
  if (!sections.length) return [];
  const first = sections[0].region.start, last = sections[sections.length - 1].region.end;
  const list = [];
  sections.forEach((x, i) => { if (i < sections.length - 1 && x.pause > 0) list.push({ at: x.region.end, seconds: x.pause }); });
  for (const c of cuts) if (c.pause > 0 && c.region.start > first && c.region.start < last) list.push({ at: c.region.start, seconds: c.pause });
  return list.sort((a, b) => a.at - b.at);
}

// keptLength is the sections without the cuts: what the outputs will last (without the pauses).
export function keptLength(sections, cuts) {
  if (!sections.length) return 0;
  const first = sections[0].region.start, last = sections[sections.length - 1].region.end;
  let length = last - first, at = first;
  for (const c of skipped(sections, cuts)) { // in time order; overlaps count once
    const from = Math.max(c.start, at), to = Math.min(c.end, last);
    if (to > from) { length -= to - from; at = to; }
  }
  return length;
}
