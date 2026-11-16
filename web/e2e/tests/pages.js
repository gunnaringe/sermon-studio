// The addresses of the studio's pages (pages.go), for the tests. Without a church they are the current
// church's: the app puts c/<church>/ in front.
const slug = (folder) => folder.split('/').pop().toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '');
const at = (church) => (church ? `c/${church}/` : '');

// recordingPage is the recordings page with the recording in folder opened; editPage is its editor.
export const recordingPage = (folder, church) => `${at(church)}recordings/${slug(folder)}`;
export const editPage = (folder, church) => `${recordingPage(folder, church)}/edit`;

// saveAndMake keeps settings as the sermon of folder (with only the formats given), saves it, and waits until the
// server has made its files.
export async function saveAndMake(request, expect, folder, settings, formats) {
  const q = '?path=' + encodeURIComponent(folder);
  const r = await request.put('api/sermon' + q, { data: { ...settings, formats } });
  expect(r.ok(), await r.text()).toBe(true);
  expect((await request.post('api/sermon/save' + q)).ok()).toBe(true);
  await expect.poll(async () => {
    const info = await (await request.get('api/folder' + q)).json();
    if (info.makeError) return 'failed: ' + info.makeError;
    if (info.making === 'waiting' || !info.job) return 'waiting';
    return info.job.state;
  }, { timeout: 90_000 }).toBe('done');
}
