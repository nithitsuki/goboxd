#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

pkg_install python3 python3-pip python3-dev python3-venv python3-psycopg2

if python3 --version >/dev/null 2>&1; then
    echo "Python3 installation verified successfully."
    python3 -c "print('Python3 is working correctly!')"
else
    echo "Python3 installation verification failed."
    exit 1
fi

mkdir -p /virtualenvs
chown root /virtualenvs
