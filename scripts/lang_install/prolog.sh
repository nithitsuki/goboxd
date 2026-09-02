#!/bin/bash
set -e
# Portable SWI-Prolog install (debian: apt pinned; arch: pacman).
source "$(dirname "$0")/helpers.sh"

pkg_update
echo "Installing SWI-Prolog..."
pkg_install swi-prolog=9.0.4+dfsg-2

if command -v swipl &> /dev/null; then
    echo "SWI-Prolog installation verified successfully."
    swipl --version | head -1
    cat > /tmp/smoke.pl <<'PL'
main :- write('prolog works: '), N is 6*7, writeln(N), halt.
:- initialization(main).
PL
    swipl -q -f /tmp/smoke.pl -t halt
else
    echo "SWI-Prolog installation verification failed"
    exit 1
fi