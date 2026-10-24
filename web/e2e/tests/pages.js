// The addresses of the studio's pages (pages.go), for the tests. Without a church they are the current
// church's: the app puts c/<church>/ in front.
const slug = (folder) => folder.split('/').pop().toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '');
const at = (church) => (church ? `c/${church}/` : '');

// recordingPage is the recordings page with the recording in folder opened; editPage is its editor.
export const recordingPage = (folder, church) => `${at(church)}recordings/${slug(folder)}`;
export const editPage = (folder, church) => `${recordingPage(folder, church)}/edit`;
