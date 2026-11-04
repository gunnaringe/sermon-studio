# Hosting it on a server

How sermon-studio.app runs, and how to run the same on a server of your own: one Linux machine, every app in
rootless Podman as its own user, and a Cloudflare Tunnel as the only way in. The files are in
[`deploy/`](../deploy/); nothing in them is particular to one host, and a host's own settings and secrets
stay on it, in `/etc/sermon-studio/`.

| Service | User | On `127.0.0.1` | Its data | Address |
|---|---|---|---|---|
| `sermon-studio` | sermon-studio | 8080, 8081 (share page) | `/srv/sermon-studio/data` | `<church>.<domain>`, `my.`, `share.` |
| `sermon-studio-models` | sermon-studio | | the speech models, in the same data | |
| `sermon-studio-demo` | sermon-studio-demo | 8090 | none: a tmpfs, fresh every start | `demo.` |
| `pocket-id` | pocket-id | 1411 | `/srv/pocket-id/data` | `id.` |
| `cloudflared` | cloudflared | | | sends each address to its port |

## What keeps it safe

- **Nothing is open but SSH.** The firewall (ufw) lets in port 22 only; cloudflared connects out to Cloudflare,
  and the apps listen on `127.0.0.1`. So the address in `CF-Connecting-IP`, which the share page's rate limits
  count, can only come from Cloudflare.
- **Every app is its own user**, rootless: a container that is broken into gives an unprivileged user, who
  cannot read another app's data (`/srv/<user>` is that user's, mode 0750). The demo, open to everyone, is
  apart from the churches' data.
- **The containers are locked down**: a read-only image, no capabilities, `no-new-privileges`, limits on memory
  and processes.
- **What the apps may reach** ([`deploy/egress.sh`](../deploy/egress.sh)): the internet (Hugging Face for the
  models, the login provider, Wikimedia for the demo's recording), but not this machine's own ports, the
  private ranges or a cloud's metadata address. ffmpeg reads files from the public; an exploit of it gets no
  further.
- **No secrets in the repository or on GitHub.** The settings are files on the host, readable only by the app
  they belong to; `deploy.sh` runs from your own machine over SSH, so no CI job can deploy anything.
- **Updates come by themselves**: new images every 15 minutes (`podman auto-update`), Ubuntu's security
  updates every night (with a reboot when one needs it).

## Setting up a host

**1. The machine.** Ubuntu 26.04 or newer (Podman 5), with:

- a user with your SSH key and sudo without a password; SSH with keys only and no root login (a file
  `/etc/ssh/sshd_config.d/00-hardening.conf`, which wins over the provider's files: `PasswordAuthentication no`,
  `PermitRootLogin no`, `AllowUsers <you>`);
- ufw: `ufw default deny incoming`, `ufw limit 22/tcp`, `ufw enable`;
- `unattended-upgrades` with `Unattended-Upgrade::Automatic-Reboot "true"`;
- once it is set up, cloud-init turned off (`touch /etc/cloud/cloud-init.disabled`): a provider's cloud-init can
  run commands at every boot (Contabo's turns root's SSH login back on).

**2. Install:** from a checkout, `deploy/deploy.sh <ssh host>`. It installs Podman, makes the users and folders,
puts in the units and the egress rules, and starts what has its settings; at the end it lists what is not set
up yet. Run it again after every change in `deploy/`: it changes only what differs. It uses one SSH
connection (`ufw limit` refuses a seventh in 30 seconds, and an IPS may take a burst for a scan).

The examples of every settings file are in `/etc/sermon-studio/examples/` on the host. Make each one as root:
`sudo install -m 0640 -o root -g <user> /dev/stdin /etc/sermon-studio/<file>`, then the content.

**3. Pocket ID**, the login (`pocket-id.env`: its address and the key its database is encrypted with; keep a
copy of the key in a password manager). A new Pocket ID is set up by its first visitor: once the tunnel runs
(4), open `https://id.<domain>/setup` yourself at once. Then make the studio's OIDC client in it, as in
[login-pocket-id.md](login-pocket-id.md).

**4. The tunnel.** On your own machine, so Cloudflare's account certificate stays there:

```bash
cloudflared tunnel login                 # in the browser: choose the domain
cloudflared tunnel create sermon-studio  # writes ~/.cloudflared/<tunnel id>.json
ssh <host> sudo install -m 0640 -o root -g cloudflared /dev/stdin \
  /etc/sermon-studio/cloudflared/credentials.json < ~/.cloudflared/<tunnel id>.json
```

Then `cloudflared.env` on the host (`DOMAIN=<domain>`), and in Cloudflare's DNS a proxied CNAME `*` to
`<tunnel id>.cfargotunnel.com` (it carries `my.`, `id.`, `share.`, `demo.` and every church; the bare domain
stays free for a website). Run `deploy/deploy.sh <host>` again: it makes cloudflared's config
([`deploy/cloudflared.yml.in`](../deploy/cloudflared.yml.in)) and starts it.

**5. The studio** (`sermon-studio.env`: the login, the addresses, the client secret; [running.md](running.md)
has every setting). It starts once there is a church, a folder:

```bash
ssh <host> sudo -u sermon-studio mkdir -p /srv/sermon-studio/data/<church>/Taler
deploy/deploy.sh <host>
ssh <host> sudo sermon-studio-admin invite <church> --role admin   # the first admin's invite link
```

The speech models (`deploy/quadlet/sermon-studio-models.container`: the list for Norwegian and Swedish, and
large-v3-turbo for the rest; change it for your languages) arrive in the next hour or so.

## Day to day

```bash
ssh <host> sudo sermon-studio-admin church list           # churches, members, invites: access.md
ssh <host> sudo journalctl CONTAINER_NAME=sermon-studio -f  # an app's own log
ssh <host> sudo journalctl _SYSTEMD_USER_UNIT=sermon-studio.service  # its starts and stops
ssh <host> sudo systemctl --user -M sermon-studio@ restart sermon-studio
```

**Updates.** A new build of `main` (the image's `main` tag) is running within about 15 minutes; one that does
not become healthy (`/healthz`) is rolled back to the image before. Pocket ID follows its `v2` and
cloudflared its `latest`. The studio, the demo and Pocket ID have a health check; the models and cloudflared do
not, so a new image of theirs is rolled back only when it does not start. A new major version of Pocket ID is
a change in `deploy/quadlet/pocket-id.container`. A service that fails is started again, without end: every
10 seconds, or every minute for Pocket ID (a killed one holds its lock in the database for a while), the demo
and the models.

**The demo** starts afresh every night at 03:00, and with every restart. Its recording is downloaded once into
`/srv/sermon-studio-demo/seed`; an intro and outro go in its `clips/` (`demo/README.md`).

## What needs a backup

- `/srv/sermon-studio/data`: the churches, `users.json` and `sessions.json` (not `cache/` or `.models/`, which
  come back by themselves);
- `/srv/pocket-id/data`: the accounts and passkeys (a copy of a running SQLite database: `sqlite3 .backup`),
  and its `ENCRYPTION_KEY`, without which the database cannot be read;
- `/etc/sermon-studio`: the settings and the tunnel's credentials.
