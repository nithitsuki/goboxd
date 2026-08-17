#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

echo "Installing Erlang..."
pkg_install erlang-nox

if command -v erl &>/dev/null; then
    echo "Erlang installation verified successfully."
    erl -noshell -eval 'io:format("Erlang ~s is working correctly!~n", [erlang:system_info(otp_release)]), halt().'
else
    echo "Erlang installation verification failed: erl not found"
    exit 1
fi
