#!/bin/bash

set -e 

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
apt-get install -y --no-install-recommends \
    python3=3.11.2-1+b1 \
    python3-pip=23.0.1+dfsg-1 \
    python3-dev=3.11.2-1+b1 \
    python3-venv=3.11.2-1+b1 \
    python3-psycopg2=2.9.5-1+b1

if python3 --version >/dev/null 2>&1; then
    echo "Python3 installation verified successfully."
    python3 -c "print('Python3 is working correctly!')"
    python3 -c "import psycopg2; print('psycopg2 is working correctly!')"
else
    echo "Python3 installation verification failed."
    exit 1
fi

mkdir -p /virtualenvs
chown root /virtualenvs