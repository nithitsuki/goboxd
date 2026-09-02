#!/bin/bash
set -e
# Portable GNU Emacs install (elisp runtime). Debian: emacs-nox (pinned,
# headless). Arch: emacs.
source "$(dirname "$0")/helpers.sh"

pkg_update
echo "Installing Emacs (elisp)..."
pkg_install emacs-nox=1:28.2+1-15+deb12u4

if command -v emacs &> /dev/null; then
    echo "Emacs installation verified successfully."
    emacs --version | head -1
    printf '(princ "emacs works: ")\n(princ (* 6 7))\n(terpri)\n' > /tmp/smoke.el
    emacs -Q --batch --script /tmp/smoke.el
else
    echo "Emacs installation verification failed"
    exit 1
fi