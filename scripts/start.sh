#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
unit=${XDG_CONFIG_HOME:-"$HOME/.config"}/systemd/user/newspaperbot.service
install -D -m 644 "$root/deploy/newspaperbot.service" "$unit"
systemctl --user daemon-reload
exec systemctl --user enable --now newspaperbot.service
