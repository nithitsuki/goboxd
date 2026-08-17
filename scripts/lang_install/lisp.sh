#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Lisp (SBCL)..."
pkg_install sbcl curl make git

curl -O https://beta.quicklisp.org/quicklisp.lisp
sbcl --no-sysinit --no-userinit \
     --load quicklisp.lisp \
     --eval '(quicklisp-quickstart:install)' \
     --eval '(ql:add-to-init-file)' \
     --quit

if command -v sbcl &>/dev/null; then
    echo "SBCL installation verified successfully."
    sbcl --version
    sbcl --no-sysinit --no-userinit \
         --eval '(format t "Lisp is working correctly!~%")' \
         --eval '(quit)'
else
    echo "SBCL installation verification failed: sbcl not found"
    exit 1
fi
