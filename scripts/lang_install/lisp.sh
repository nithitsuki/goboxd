#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Lisp (SBCL)..."
apt-get install -y --no-install-recommends sbcl curl make git

# Install Quicklisp
curl -O https://beta.quicklisp.org/quicklisp.lisp
sbcl --no-sysinit --no-userinit \
     --load quicklisp.lisp \
     --eval '(quicklisp-quickstart:install)' \
     --eval '(ql:add-to-init-file)' \
     --quit

# Verify SBCL is working
if command -v sbcl &> /dev/null; then
    echo "SBCL installation verified successfully."
    sbcl --version
    # Run a simple verification
    sbcl --no-sysinit --no-userinit \
         --eval '(format t "Lisp is working correctly!~%")' \
         --eval '(quit)'
else
    echo "SBCL installation verification failed: sbcl not found"
    exit 1
fi
