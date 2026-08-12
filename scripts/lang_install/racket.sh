#!/bin/bash
set -e

echo "Installing Racket..."
# The racket installer bakes absolute paths into config.rktd and the
# bin/* wrappers (the racket binary itself embeds them), so the install
# must go directly to /usr/local/racket. Only the download is cached.
DL=/var/cache/goboxd-dl
mkdir -p "$DL"

[ -f "$DL/racket-install.sh" ] || curl -fsSL -o "$DL/racket-install.sh" \
    https://download.racket-lang.org/releases/8.15/installers/racket-8.15-x86_64-linux-cs.sh

rm -rf /usr/local/racket
sh "$DL/racket-install.sh" --unix-style --dest /usr/local/racket --create-dir

ln -sf /usr/local/racket/bin/racket /usr/local/bin/racket

echo "Racket installation verified: $(/usr/local/bin/racket --version)"
printf '#lang racket\n(displayln "racket smoke test")\n' > /tmp/smoke.rkt
/usr/local/bin/racket /tmp/smoke.rkt
