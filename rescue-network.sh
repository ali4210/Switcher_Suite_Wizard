#!/usr/bin/env bash
# ==============================================================================
#           SWITCHER SUITE WIZARD — OFFLINE EMERGENCY RESCUE SCRIPT
# ==============================================================================
# This script requires NO Go compiler, NO compilation, and NO internet connection.
# It automatically restores broken routing, missing gateways, and failed DNS.

if [ "$EUID" -ne 0 ]; then
    echo "[!] Elevation required. Re-running with sudo..."
    exec sudo bash "$0" "$@"
fi

echo "--- [1/5] Restoring /etc/hosts ---"
CURRENT_HOST=$(hostname 2>/dev/null || echo "localhost")
printf "127.0.0.1\tlocalhost %s\n::1\t\tlocalhost ip6-localhost ip6-loopback\n127.0.1.1\t%s\n" "$CURRENT_HOST" "$CURRENT_HOST" > /etc/hosts
echo "[✓] /etc/hosts mapped for hostname: $CURRENT_HOST"

echo "--- [2/5] Identifying Primary Network Interface ---"
PRIMARY_IFACE=$(ip -4 -o addr show | awk '$2 != "lo" && $2 !~ /^docker/ {print $2; exit}')
if [ -z "$PRIMARY_IFACE" ]; then
    PRIMARY_IFACE="eth0"
fi
echo "[✓] Detected Primary Interface: $PRIMARY_IFACE"

echo "--- [3/5] Re-establishing Default Gateway Route ---"
# Check if default route exists; if missing, inject router IP
if ! ip route show | grep -q "default via"; then
    echo "[!] Default route missing. Adding default via 192.168.0.1 dev $PRIMARY_IFACE..."
    ip route add default via 192.168.0.1 dev "$PRIMARY_IFACE" 2>/dev/null || true
fi

echo "--- [4/5] Restoring Fallback DNS Resolvers ---"
printf "nameserver 1.1.1.1\nnameserver 8.8.8.8\nnameserver 192.168.0.1\n" > /etc/resolv.conf
echo "[✓] /etc/resolv.conf updated with Cloudflare, Google, and Local Router DNS."

echo "--- [5/5] Re-initializing NetworkManager Connection ---"
if command -v nmcli &>/dev/null; then
    CONN_NAME=$(nmcli -g GENERAL.CONNECTION device show "$PRIMARY_IFACE" 2>/dev/null | head -n 1)
    if [ -n "$CONN_NAME" ] && [ "$CONN_NAME" != "--" ]; then
        echo "[+] Resetting Connection '$CONN_NAME' to Auto DHCP..."
        nmcli connection modify "$CONN_NAME" ipv4.method auto ipv4.gateway "" ipv4.dns "" ipv4.ignore-auto-dns no 2>/dev/null || true
        nmcli connection up "$CONN_NAME" 2>/dev/null || true
    else
        nmcli device connect "$PRIMARY_IFACE" 2>/dev/null || true
    fi
fi

echo ""
echo "=============================================================================="
echo "                  NETWORK SELF-HEALING COMPLETE"
echo "=============================================================================="
ip route show
echo ""
echo "[+] Testing Connectivity:"
ping -c 2 1.1.1.1 2>/dev/null && echo "[✓] Internet Layer 3 Ping: SUCCESS" || echo "[!] Internet Ping: FAILED (Check physical router)"
ping -c 2 google.com 2>/dev/null && echo "[✓] DNS Name Resolution: SUCCESS" || echo "[!] DNS Resolution: FAILED"