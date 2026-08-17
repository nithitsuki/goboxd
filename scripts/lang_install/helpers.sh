#!/bin/bash
# helpers.sh — portable package management for debian and arch.
# Source this file from any lang_install script.
#
# Usage:
#   source "$(dirname "$0")/helpers.sh"
#   pkg_update
#   pkg_install gcc g++ base-devel

set -euo pipefail

# Detect the package manager once per shell session.
_detect_pkg_manager() {
    if [ -n "${_PKG_MANAGER+x}" ]; then
        return
    fi
    if command -v apt-get &>/dev/null; then
        _PKG_MANAGER=apt
        _OS=debian
    elif command -v pacman &>/dev/null; then
        _PKG_MANAGER=pacman
        _OS=arch
    else
        echo "helpers.sh: no supported package manager found (apt-get or pacman)" >&2
        exit 1
    fi
}

# Update the package index (no-op on pacman).
pkg_update() {
    _detect_pkg_manager
    if [ "$_PKG_MANAGER" = "apt" ]; then
        apt-get update -qq || (sleep 10 && apt-get update -qq) || (sleep 30 && apt-get update -qq)
    fi
}

# Install packages. Accepts the same names as the debian scripts; on arch
# the names are transparently mapped. Version pins (e.g. gcc=12.2) are
# stripped on arch where pacman does not support them.
pkg_install() {
    _detect_pkg_manager
    if [ "$_PKG_MANAGER" = "apt" ]; then
        apt-get install -y --no-install-recommends "$@"
    else
        # Strip version pins (name=X.Y.Z) — arch installs the repo version.
        local clean=()
        for pkg in "$@"; do
            clean+=("${pkg%%=*}")
        done
        pacman -S --needed --noconfirm "${clean[@]}"
    fi
}

# Return "debian" or "arch".
pkg_os() {
    _detect_pkg_manager
    echo "$_OS"
}
