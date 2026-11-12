# The website: sermon-studio.app

What Sermon Studio is, how it works, the guide in short, privacy, and the desktop app's download, in English,
bokmål and nynorsk: English at `/`, bokmål under `/nb/`, nynorsk under `/nn/`. Plain HTML and CSS with a little
JavaScript (`site.js`); every page has its text in it, so it reads without JavaScript and search engines see it. The
pages are made from `src/` by `node site/src/build.mjs` and committed: the Worker has no build step. It loads nothing
from other hosts (`_headers` sets a Content-Security-Policy that keeps it so).

| | |
|---|---|
| `src/index.html`, `guide.html`, `privacy.html`, `404.html` | the pages' templates: the markup, with `data-t="key"` where a text goes, `data-t-<attribute>="key"` for an attribute, `data-shot="name"` for a picture of the studio; addresses as from the site's root. The header has the front page's sections and the guide (a page of its own: a line and a book before it); the footer is About, as in the studio, with privacy among its links |
| `src/lang/en.json`, `nb.json`, `nn.json` | all the texts, one file per language, with the same keys |
| `src/build.mjs` | makes the pages from the two above: `index.html`, `guide.html`, `privacy.html`, `404.html` here and under `nb/` and `nn/` (each with its canonical address, the other languages as `hreflang`, and its link preview in its language), and `sitemap.xml`. **Never change those by hand.** `--check` says which differ |
| `robots.txt` | points search engines to `sitemap.xml` |
| `site.js` | sends a reader of an English page to their language's page (`?lang=`, else the one chosen in the language menu before, else the browser's; a page under `/nb/` or `/nn/` stays as asked for), remembers a choice in the language menu (the globe: links), the header's menu on a tablet or phone, the section in view marked in the header, a picture shown larger |
| `style.css` | the studio's look: neutral grey with its green as the accent, light and dark as the device is |
| `fonts/signature.woff2` | the signature in About, a copy of `web/static/fonts/signature.woff2` (not under the licence: see `NOTICE`) |
| `img/logo.svg`, `img/*.png` | the studio's logo and icons, copied from `web/static/icons/` |
| `img/og.png` | the picture of a link preview (Signal, Slack…; `og:image` in each page's head, with the page's own title and description), made by `scripts/make-og-image.sh` from `scripts/og-card.html` |
| `img/shots/<lang>/*.webp` | the studio in each language, made by `scripts/site-shots.sh shots` |
| `media/demo.mp4`, `demo.webm`, `demo-poster.webp` | the video, in English, made by `scripts/site-shots.sh video` |

**Log in** goes to the hosted studio's login host (https://my.sermon-studio.app/), whose start offers Log in and
Create an account, and **Ask for an invite** writes to contact@sermon-studio.app.

## Changing it

- After any change in `src/`: `node site/src/build.mjs`, and commit what it makes with it (the test fails otherwise).
- A new text: add its key to all three files in `src/lang/`. A new picture of the studio: add it to
  `web/e2e/site-shots/shots.spec.js` and run `scripts/site-shots.sh shots`.
- When the studio looks different: `scripts/site-shots.sh` makes all the pictures and the video again (about 10
  minutes, in podman; the speech comes from `scripts/fetch-sample.sh`).
- Check it: `scripts/test.sh site` serves this folder and checks every page in every language, the language menu, the phone's menu,
  that the languages have the same keys, every link and picture, and the video. It saves screenshots of the
  pages in `web/e2e/screenshots/site-*.png` for the PR.

## Deploying

A Cloudflare Worker with static assets only, `sermon-studio-site` (`wrangler.jsonc`), deployed by Workers
Builds on every push to `main` that changes `site/`. Set up once, in the Cloudflare dashboard:

1. **Workers & Pages → Create → Import a repository**: `gunnaringe/sermon-studio`.
2. Project name `sermon-studio-site` (as in `wrangler.jsonc`). **Root directory** `site`. No build command.
   Deploy command `npx wrangler deploy` (the default).
3. In the Worker's **Settings → Build**: production branch `main`, and **Build watch paths**: include `site/**`,
   so changes to the studio itself do not deploy the site.
4. **Settings → Domains & Routes → Add → Custom domain**: `sermon-studio.app`, then `www.sermon-studio.app`
   (the zone must be on the same Cloudflare account). Both show the site. To send www to the bare domain
   instead, add a redirect rule for `www.sermon-studio.app/*` → `https://sermon-studio.app/${1}` (301).

`.assetsignore` keeps `wrangler.jsonc`, this README, `src/` and wrangler's own files from being served.
