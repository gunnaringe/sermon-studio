#!/bin/sh
# Installs or updates Sermon Studio on a machine from this checkout: deploy/deploy.sh <ssh host>
# (docs/hosting.md). It sends deploy/ and runs deploy/install.sh there with sudo, over one SSH connection: a
# firewall that limits new SSH connections (ufw's limit) or an IPS that takes a burst of them for a scan
# would block more. The user needs sudo without a password, or is root.
set -eu
host=${1:?usage: deploy/deploy.sh <ssh host>}
cd "$(dirname "$0")"
tar -czf - --exclude=./deploy.sh . | ssh "$host" '
  set -e
  d=$(mktemp -d)
  trap "rm -rf \"$d\"" EXIT
  tar -xzf - -C "$d"
  sudo -n "$d/install.sh"'
