#!/bin/bash
# Entrypoint for the goboxd container.
#
# Applies an egress firewall before starting the server: every NEW outbound
# connection is dropped, so the container cannot reach the internet from
# inside (no RCE / persistence / data exfiltration path). Inbound API
# requests still work because their responses match ESTABLISHED/RELATED
# connections.
#
# If the firewall cannot be applied, the container refuses to start.
# Fail closed: the service must never run with internet egress.
set -e

if iptables -P OUTPUT DROP 2>/dev/null; then
    iptables -A OUTPUT -o lo -j ACCEPT
    iptables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    iptables -A OUTPUT -j DROP
else
    echo "ERROR: cannot apply egress firewall (iptables failed)." >&2
    echo "The container must run with the CAP_NET_ADMIN capability." >&2
    echo "Refusing to start without network isolation." >&2
    exit 1
fi

exec /usr/local/bin/goboxd "$@"
