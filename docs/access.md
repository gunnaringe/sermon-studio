# Access: which churches a user sees

There are two ways to run the studio, and one of them must be chosen: the studio does not start without
`--oidc-issuer` or `--no-login` (and not with both), so a missing or misspelt setting never means open access.

- **With its own login** (`--oidc-issuer`, below): a church is for its members, who join with an invite link.
- **Without** (`--no-login`): no access control. Everyone sees every church and can add churches (the desktop app, a trusted
  network, or behind a login proxy that lets only the right people through).
- **A demo** (`--no-login --visitor-role demo`): everyone sees every church as a demo's visitor: they edit, make and
  download the files, and nothing else (no upload, deleting, shares, settings or adding churches). The page says it is a
  demo. See [A public demo](#a-public-demo).

## Its own login (OpenID Connect)

The studio logs users in with any OpenID Connect provider (Pocket ID, Authentik, Keycloak, Google, Entra …).
[Logging in with Pocket ID](login-pocket-id.md) is the recommended setup, step by step, with other providers at
the end. Register the studio there as a client with the callback `<public-url>/auth/callback` and the logout
callback `<public-url>/auth/logout/callback` (PKCE; a client secret is optional), and start it with:

```bash
sermon-studio --data-dir /data --oidc-issuer https://id.example.org --oidc-client-id <id> \
  --oidc-client-secret-file /run/secrets/oidc --public-url https://sermons.example.org/
```

- A user is the provider's issuer and `sub`, nothing else. Name and email from the login are shown, never used
  to give access. Users are kept in `<data-dir>/users.json`, sessions (30 days, only their hashes) in `sessions.json`.
- Every page and every API call needs a session: a page goes to the login and comes back, the API answers 401.
  Health checks use `/healthz`, which is open.
- A church's members are in `<church>/members.json`, each an **admin** or a **member**. Members use the studio:
  recordings, editing, runs, downloads and shares. Admins also change the church's settings (name, logo, tags,
  processing, intro and outro clips, how long ended shares are kept) and who its members are. A user who is no
  church's member is told to ask for an invite link.
- **What a role may do** is a set of actions, listed in `web/permissions.go`:

  | Action | What it is | Admin | Member | Demo |
  |---|---|---|---|---|
  | `church.view` | see the church: its recordings, settings and runs | ✓ | ✓ | ✓ |
  | `recording.upload` | add a recording, or a sermon's own intro or outro | ✓ | ✓ | |
  | `recording.edit` | cut, tag, rename, hear a sample | ✓ | ✓ | ✓ |
  | `recording.make` | make the files, stop a run | ✓ | ✓ | ✓ |
  | `recording.download` | listen to and download the files | ✓ | ✓ | ✓ |
  | `recording.delete` | delete files, originals and recordings; the trash | ✓ | ✓ | |
  | `shares.manage` | the church's shares | ✓ | ✓ | |
  | `church.settings` | name, logo, default tags, processing, how long ended shares are kept | ✓ | | |
  | `church.clips` | the church's intro and outro clips | ✓ | | |
  | `members.manage` | the members and invite links | ✓ | | |

  Every request is one of these, and is refused (403) when the role does not have it; the page shows only what the
  user may do. Without the login, everyone is an admin, unless `--visitor-role` says otherwise.
- **Settings → Members** (admins): the members with their name and email from the login, their role, Remove; invite
  links (role, 1, 7 or 30 days), shown once with a copy button; the open invites, with Revoke. A member sees only
  **Leave this church…**. A church always keeps an admin: the last one cannot step down, leave or be removed.
- **Invite links** are single use and kept only as a hash, in `<church>/invites.json`. Opening one logs in and asks
  to accept; accepting makes that login a member (an admin invite makes a member an admin).
- **Churches** are added by the site administrator, with `sermon-studio admin` (below), not in the studio.
- **Log out** ends the studio's session, and then the login at the provider too, when the provider supports it
  (OpenID Connect RP-initiated logout: an `end_session_endpoint`). The browser goes there and comes back to the
  studio's start, which offers Log in. For that the ID token from the login is kept with the session in `sessions.json`,
  never in a cookie. A provider without it leaves its own login as it is.

## Each church at its own address

With `--church-host '{church}.sermon-studio.app'` every church has a host of its own
(`norkirken-melhus.sermon-studio.app`), and the login is on the host of `--public-url`:

```bash
sermon-studio --data-dir /data --oidc-issuer https://id.example.org --oidc-client-id <id> \
  --oidc-client-secret-file /run/secrets/oidc --public-url https://my.sermon-studio.app/ \
  --cookie-domain sermon-studio.app --church-host '{church}.sermon-studio.app'
```

- **The church is the host's**, and the user must be one of its members: asking a church's host for another
  church, or the login host for any, finds nothing. An unknown church's host says so.
- **One login for all of them**: the session cookie is set on `--cookie-domain`, which both the login host and the
  church hosts must be under (checked at start). A page on a church host goes to the login on the login host and
  comes back to where it was; the way back may only be the login host or a church host, with the same scheme and port.
- **Log out** on any host ends the session everywhere and comes back through the login host, so the provider still
  has exactly two addresses: `<public-url>/auth/callback` and `<public-url>/auth/logout/callback`.
- **The login host** serves the login, invite links (`<public-url>/invite#…`, which take you to the church's
  host) and the start (`/`, which goes straight to your church: the one you opened last, kept in the cookie `studio_church` on the cookie domain, else the first; logged
  out, it is the start, with Log in and, with `--oidc-signup-url`, Create an account).
- The church is known from the host, so it is not in the page's address (`norkirken-melhus.sermon-studio.app/settings`);
  an address with `c/<church>/` in it goes to that church's host.
- Church ids the site's own hosts may have are reserved: `app`, `my`, `share`, `www`, `id`, `api`, `auth`, `login`, `demo`, `mail`,
  `admin`, `static`, `status`, `docs`, `help`.
- The public share page keeps its own host (`--share-listen`, `--share-url`, e.g. `share.sermon-studio.app`).

## The site administrator: `sermon-studio admin`

Run where the studio runs, with the same data dir (`--data-dir`, or `SERMON_STUDIO_DATA_DIR`, which the container
already has when it is set that way). The commands talk to the running studio over its admin socket,
`<data-dir>/.admin.sock` (only the studio's own user can use it, never the network), so a change takes effect at once,
with the same checks as in the studio. `--json` prints JSON for scripts.

```bash
sermon-studio admin church create norkirken-melhus --name "Norkirken Melhus"   # prints an invite for its first admin
sermon-studio admin church list                                          # with what each keeps, and its quota
sermon-studio admin church rename norkirken-melhus "Norkirken"
sermon-studio admin church quota norkirken-melhus 50GB                    # how much it may keep; none = no limit
sermon-studio admin members norkirken-melhus
sermon-studio admin invite norkirken-melhus --role admin --expires 7d   # also how to get back in when a church has lost its admins
sermon-studio admin remove norkirken-melhus <id or email>               # the id is in `admin members`
# on Kubernetes: kubectl exec deploy/sermon-studio -- sermon-studio admin church list
```

A church's quota is the site administrator's: it is kept in `<data-dir>/churches.json`, and the church's own admins see
it, and what the church keeps by kind (recordings with their waveform, made files, intro and outro clips, the trash), in
Settings → Church → Storage, with a warning from 90 %. Sizes are in binary units, as the studio shows them (1 GB =
1024 MB). Files in the inbox count once they are taken in.

A church always keeps an admin: removing its last one is refused (invite a new admin first). Only one studio runs on a
data dir: a second one finds the first's socket and refuses to start. Without the login there are no members or
invites, and churches are also added in the studio.

Without `--oidc-issuer` the server has no login of its own: run it on the LAN, or behind a proxy that handles login.
It uses relative URLs (works under a sub-path), refuses cross-site POSTs, keeps every path
inside `--root`, and has no read/write timeouts (long uploads and live updates). On SIGTERM it
cancels running jobs and waits for them to clean up.

## A public demo

`--visitor-role demo` (with `--no-login`) gives every visitor the demo role above; `--visitor-role` takes any role
(`admin`, the default, `member`, `demo`). A demo keeps nothing for long: run it on a data dir that starts empty, and
start it again to reset it. With a church folder and a recording in its inbox, named by its date, it is ready when the
inbox has filed it (within seconds when the file keeps an old time, `cp -p`):

```sh
mkdir -p /data/demo-church/Taler /data/demo-church/Innboks
printf '{"name":"Demo"}' > /data/demo-church/church.json
cp -p /seed/speech.mp3 /data/demo-church/Innboks/2026-01-15.mp3
sermon-studio --data-dir /data --no-login --visitor-role demo
```

`demo` is a reserved church id (the demo's host), so the church needs another.
