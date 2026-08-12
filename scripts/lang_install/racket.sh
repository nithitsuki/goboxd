#!/bin/bash
set -e

echo "Installing Racket (CS)..."
curl -fsSL -o /tmp/racket-install.sh \
    https://download.racket-lang.org/releases/8.15/installers/racket-8.15-x86_64-linux-cs.sh

sh /tmp/racket-install.sh --unix-style --dest /usr/local/racket --create-dir

ln -sf /usr/local/racket/bin/racket /usr/local/bin/racket

if command -v racket &> /dev/null; then
    echo "Racket installation verified successfully."
    racket --version
    racket -e '(displayln "Racket is working correctly!")'
else
    echo "Racket installation verification failed: racket not found"
    exit 1
fi
