#!/bin/sh
# What the apps may reach (systemd/sermon-studio-egress.service): the internet, but not this machine, the
# private ranges or a cloud's metadata address. ffmpeg reads files from the public, and an exploit of it
# must not reach the other apps' ports, SSH, or anything else on the provider's network.
#
# Rootless containers reach the network through pasta, which runs as the app's own user, so the rules match
# the apps' users (meta skuid). Replies to connections that came in (cloudflared to the published ports), DNS
# to systemd-resolved and IPv6 neighbour discovery stay allowed. Its own table, so ufw's rules are left alone; loaded again, it
# replaces itself. The machine's own addresses are read when it is loaded.
set -eu

uids=''
for u in sermon-studio sermon-studio-demo pocket-id; do
  if id -u "$u" >/dev/null 2>&1; then uids="$uids${uids:+, }$(id -u "$u")"; fi
done
[ -n "$uids" ] || { echo "egress: none of the apps' users exist" >&2; exit 1; }
addrs() { ip "-$1" -o addr show scope global | awk '{ sub("/.*", "", $4); print $4 }' | paste -sd, -; }
v4=$(addrs 4)
v6=$(addrs 6)

nft -f - <<EOF
table inet sermon_studio_egress
delete table inet sermon_studio_egress
table inet sermon_studio_egress {
  chain output {
    type filter hook output priority filter; policy accept;
    ct state established,related accept
    meta skuid != { $uids } accept
    ip daddr { 127.0.0.53, 127.0.0.54 } meta l4proto { tcp, udp } th dport 53 accept
    # Finding the IPv6 gateway: the kernel sends it on behalf of the app's connection (to ff02::1:ff…).
    icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert, mld2-listener-report } accept
    ip daddr { 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 169.254.0.0/16, 224.0.0.0/4${v4:+, $v4} } counter reject
    ip6 daddr { ::1, fc00::/7, fe80::/10, ff00::/8${v6:+, $v6} } counter reject
  }
}
EOF
