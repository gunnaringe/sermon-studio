#!/bin/bash
# Installs or updates Sermon Studio on this machine, as root; deploy/deploy.sh runs it from a checkout
# (docs/hosting.md). Ubuntu 26.04 or newer (Podman 5). Safe to run again: it never changes the data in /srv or
# the settings in /etc/sermon-studio, and restarts only the apps whose files changed.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
[ "$(id -u)" = 0 ] || { echo "install.sh: run as root" >&2; exit 1; }

# Each app runs rootless as its own user, its data in /srv/<user>.
apps=(sermon-studio sermon-studio-demo pocket-id cloudflared)
declare -A owner=(
  [sermon-studio.container]=sermon-studio
  [sermon-studio-models.container]=sermon-studio
  [sermon-studio-demo.container]=sermon-studio-demo
  [pocket-id.container]=pocket-id
  [cloudflared.container]=cloudflared
)
declare -A restart=() # user → units whose files changed

step() { echo "== $*"; }
# put <src> <dest> <mode> [owner:group]: installs a file when it differs; true when it did.
put() {
  if [ -e "$2" ] && cmp -s "$1" "$2"; then return 1; fi
  if [ -n "${4:-}" ]; then
    install -D -m "$3" -o "${4%%:*}" -g "${4##*:}" "$1" "$2"
  else
    install -D -m "$3" "$1" "$2"
  fi
  echo "   $2"
}
# as <user> <command...>: in that user's own systemd (its containers are rootless).
as() {
  local uid; uid=$(id -u "$1")
  runuser -u "$1" -- env XDG_RUNTIME_DIR="/run/user/$uid" DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$uid/bus" "${@:2}"
}

step packages
export DEBIAN_FRONTEND=noninteractive
need=()
for p in podman passt uidmap nftables gettext-base; do dpkg -s "$p" >/dev/null 2>&1 || need+=("$p"); done
if ((${#need[@]})); then apt-get update -qq && apt-get install -y -qq "${need[@]}" >/dev/null; fi
echo "   $(podman --version)"

step users
for u in "${apps[@]}"; do
  id "$u" >/dev/null 2>&1 || { useradd --create-home --home-dir "/srv/$u" --shell /usr/sbin/nologin --user-group "$u"; echo "   $u"; }
  loginctl enable-linger "$u"
done
install -d -o sermon-studio -g sermon-studio -m 0750 /srv/sermon-studio/data
install -d -o sermon-studio-demo -g sermon-studio-demo -m 0750 /srv/sermon-studio-demo/seed
install -d -o pocket-id -g pocket-id -m 0750 /srv/pocket-id/data

step settings
install -d -m 0755 /etc/sermon-studio /etc/sermon-studio/examples
install -d -o root -g cloudflared -m 0750 /etc/sermon-studio/cloudflared
for f in "$here"/env/*.example; do put "$f" "/etc/sermon-studio/examples/$(basename "$f")" 0644 || true; done
# The settings themselves are made by hand from the examples; here only their owner and mode.
for app in sermon-studio pocket-id; do
  f=/etc/sermon-studio/$app.env
  if [ -e "$f" ]; then chown "root:$app" "$f"; chmod 0640 "$f"; fi
done
creds=/etc/sermon-studio/cloudflared/credentials.json
if [ -e "$creds" ]; then chown root:cloudflared "$creds"; chmod 0640 "$creds"; fi
if [ -e /etc/sermon-studio/cloudflared.env ] && [ -e "$creds" ]; then
  DOMAIN=$(sed -n 's/^DOMAIN=//p' /etc/sermon-studio/cloudflared.env)
  TUNNEL_ID=$(sed -n 's/.*"TunnelID" *: *"\([^"]*\)".*/\1/p' "$creds")
  [ -n "$DOMAIN" ] && [ -n "$TUNNEL_ID" ] || { echo "install.sh: no DOMAIN in cloudflared.env or no TunnelID in $creds" >&2; exit 1; }
  export DOMAIN TUNNEL_ID
  tmp=$(mktemp); envsubst '${DOMAIN} ${TUNNEL_ID}' <"$here/cloudflared.yml.in" >"$tmp"
  if put "$tmp" /etc/sermon-studio/cloudflared/config.yml 0640 root:cloudflared; then restart[cloudflared]+=" cloudflared.service"; fi
  rm -f "$tmp"
fi

step programs
put "$here/egress.sh" /usr/local/lib/sermon-studio/egress.sh 0755 && egress_changed=1 || egress_changed=
put "$here/demo-start.sh" /usr/local/lib/sermon-studio/demo-start.sh 0755 && restart[sermon-studio-demo]+=" sermon-studio-demo.service" || true
put "$here/sermon-studio-admin" /usr/local/bin/sermon-studio-admin 0755 || true

step units
for f in "$here"/quadlet/*.container; do
  name=$(basename "$f"); u=${owner[$name]:?"no user for $name"}
  if put "$f" "/etc/containers/systemd/users/$(id -u "$u")/$name" 0644; then restart[$u]+=" ${name%.container}.service"; fi
done
put "$here/systemd/sermon-studio-demo-reset.service" /etc/systemd/user/sermon-studio-demo-reset.service 0644 || true
put "$here/systemd/sermon-studio-demo-reset.timer" /etc/systemd/user/sermon-studio-demo-reset.timer 0644 || true
put "$here/systemd/podman-auto-update.conf" /etc/systemd/user/podman-auto-update.timer.d/sermon-studio.conf 0644 || true
systemctl --global enable --quiet podman-auto-update.timer sermon-studio-demo-reset.timer
put "$here/systemd/sermon-studio-egress.service" /etc/systemd/system/sermon-studio-egress.service 0644 && egress_changed=1 || true
systemctl daemon-reload
systemctl enable --quiet sermon-studio-egress.service
if [ -n "$egress_changed" ]; then systemctl restart sermon-studio-egress.service; else systemctl start sermon-studio-egress.service; fi

step apps
for u in "${apps[@]}"; do
  systemctl start "user@$(id -u "$u").service"
  as "$u" systemctl --user daemon-reload
  as "$u" systemctl --user start podman-auto-update.timer
  [ "$u" = sermon-studio-demo ] && as "$u" systemctl --user start sermon-studio-demo-reset.timer
  for unit in $(cd /etc/containers/systemd/users/"$(id -u "$u")" && ls *.container | sed 's/\.container$/.service/'); do
    if [[ " ${restart[$u]:-} " == *" $unit "* ]]; then verb=restart; else verb=start; fi
    # A unit whose settings are not there yet is skipped by its ConditionPathExists, not failed.
    as "$u" systemctl --user "$verb" "$unit" ||
      echo "   $unit did not start: journalctl _SYSTEMD_USER_UNIT=$unit, and CONTAINER_NAME=${unit%.service} for its output" >&2
  done
done

step status
for u in "${apps[@]}"; do
  for f in /etc/containers/systemd/users/"$(id -u "$u")"/*.container; do
    unit=$(basename "$f" .container).service
    printf '   %-32s %s\n' "$unit" "$(as "$u" systemctl --user is-active "$unit" || true)"
  done
done
for f in sermon-studio.env pocket-id.env cloudflared.env cloudflared/credentials.json; do
  [ -e "/etc/sermon-studio/$f" ] || echo "   not set up yet: /etc/sermon-studio/$f (docs/hosting.md)"
done
