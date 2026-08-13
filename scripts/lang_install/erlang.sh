#!/bin/bash
set -e

# Ensure apt package lists are present. The /var/lib/apt/lists cache mount
# can be cold after a Dockerfile change; a warm mount skips the network update.
[ -n "$(ls -A /var/lib/apt/lists 2>/dev/null)" ] || apt-get update
echo "Installing Erlang..."
# erlang-base provides /usr/bin/erl and /usr/bin/erlc. The full `erlang`
# metapackage would pull many optional modules; elixir.sh installs the
# modules it needs.
apt-get install -y --no-install-recommends erlang-base=1:25.2.3+dfsg-1+deb12u4

# Verify Erlang is working
if command -v erl &> /dev/null; then
    echo "Erlang installation verified successfully."
    erl -noshell -eval 'io:format("Erlang ~s is working correctly!~n", [erlang:system_info(otp_release)]), halt().'
else
    echo "Erlang installation verification failed: erl not found"
    exit 1
fi
