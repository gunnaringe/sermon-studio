#!/bin/sh
# Makes site/img/og.png, the picture link previews show (og:image), from scripts/og-card.html.
# Run it after changing the card, the logo or the editor picture, and commit the PNG.
#   needs: Chrome or Chromium; oxipng if installed
set -eu
cd "$(dirname "$0")/.."
chrome=$(command -v chromium || command -v chromium-browser || command -v google-chrome)
"$chrome" --headless --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1200,630 \
	--allow-file-access-from-files --screenshot="$PWD/site/img/og.png" "file://$PWD/scripts/og-card.html" 2>/dev/null
if command -v oxipng >/dev/null; then oxipng -q -o 4 --strip safe site/img/og.png; fi
ls -l site/img/og.png
