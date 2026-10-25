// The start (/, where the address is not a church's own host): it goes
// straight to a church of the user, the one last opened (kept in this
// browser, or by the server's studio_church cookie for church hosts), else the
// first; with none it says how to get in. Logged out, it (also after logging
// out) offers Log in and, when the provider lets people make an account
// (--oidc-signup-url), Create an account.
import { h, toLogin, churchURL } from './util.js';
import { t, tr, lang, LANGS, setLang } from './i18n.js';

const langSelect = document.getElementById('lang');
langSelect.replaceChildren(...LANGS.map(([code, name]) => h('option', { value: code, selected: code === lang }, name)));
langSelect.addEventListener('change', () => { setLang(langSelect.value); location.reload(); });

const view = document.getElementById('view');

async function start() {
  const resp = await fetch('api/tenants');
  const data = await resp.json().catch(() => ({}));
  if (resp.status === 401 && data.login) {
    welcome(data.signup);
    return;
  }
  if (!resp.ok) {
    view.replaceChildren(h('h1', {}, t('Your churches')), h('p', {}, tr(data.error || `${resp.status} ${resp.statusText}`)));
    return;
  }
  const last = localStorage.getItem('tenant');
  const church = data.tenants.find((c) => c.id === last) || data.tenants.find((c) => c.id === data.default) || data.tenants[0];
  location.replace(churchURL(church));
}

// welcome is the start for someone who is not logged in.
// After a login that did not work (?failed), it says so.
function welcome(signup) {
  view.replaceChildren(h('h1', {}, t('Welcome to Sermon Studio')),
    location.search === '?failed' ? h('p', { class: 'error' }, t('The login did not work. Try again, or ask the church\'s admin if it keeps failing.')) : '',
    h('p', {}, t('Log in to open the recordings of your church.')),
    h('p', { class: 'choices' },
      h('button', { class: 'primary', type: 'button', name: 'login', onclick: toLogin }, t('Log in')), ' ',
      signup ? h('a', { class: 'button', href: signup, target: '_blank', rel: 'noopener', name: 'signup' }, t('Create an account')) : ''),
    signup ? h('p', { class: 'muted' }, t('New here? Create an account in the tab that opens, and make a passkey when you are asked to. Then come back to this tab and press Log in.')) : '',
    h('p', { class: 'muted' }, t('Invited? Open the invite link you got.')));
}

start().catch((err) => view.replaceChildren(h('p', { class: 'error' }, err.message)));
