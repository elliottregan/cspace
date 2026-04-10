#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

# Firewall initialization script for cspace devcontainers.
# Applies an iptables allowlist: GitHub IPs, base domains, and
# project-specific domains from CSPACE_FIREWALL_DOMAINS env var.
#
# Based on Anthropic's Claude Code implementation.

echo "Initializing firewall..."

# 1. Extract Docker DNS info BEFORE any flushing
DOCKER_DNS_RULES=$(iptables-save -t nat | grep "127\.0\.0\.11" || true)

# Set permissive policies BEFORE flushing so we don't lock ourselves out
iptables -P INPUT ACCEPT
iptables -P OUTPUT ACCEPT
iptables -P FORWARD ACCEPT

# Flush existing rules and delete existing ipsets
iptables -F
iptables -X
iptables -t nat -F
iptables -t nat -X
iptables -t mangle -F
iptables -t mangle -X
ipset destroy allowed-domains 2>/dev/null || true

# 2. Selectively restore ONLY internal Docker DNS resolution
if [ -n "$DOCKER_DNS_RULES" ]; then
  echo "Restoring Docker DNS rules..."
  iptables -t nat -N DOCKER_OUTPUT 2>/dev/null || true
  iptables -t nat -N DOCKER_POSTROUTING 2>/dev/null || true
  while IFS= read -r rule; do
    if [ -n "$rule" ]; then
      IFS=' ' read -ra args <<< "$rule"
      iptables -t nat "${args[@]}" 2>/dev/null || true
    fi
  done <<< "$DOCKER_DNS_RULES"
else
  echo "No Docker DNS rules to restore"
fi

# Allow DNS and localhost before any restrictions
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
iptables -A INPUT -p udp --sport 53 -j ACCEPT
iptables -A OUTPUT -p tcp --dport 22 -j ACCEPT
iptables -A INPUT -p tcp --sport 22 -m state --state ESTABLISHED -j ACCEPT
iptables -A INPUT -i lo -j ACCEPT
iptables -A OUTPUT -o lo -j ACCEPT

# Create ipset with CIDR support
ipset create allowed-domains hash:net

# Fetch GitHub meta information and add their IP ranges
echo "Fetching GitHub IP ranges..."
gh_ranges=$(curl -s https://api.github.com/meta)
if [ -z "$gh_ranges" ]; then
  echo "ERROR: Failed to fetch GitHub IP ranges"
  exit 1
fi

if ! echo "$gh_ranges" | jq -e '.web and .api and .git' >/dev/null; then
  echo "ERROR: GitHub API response missing required fields"
  exit 1
fi

echo "Processing GitHub IPs..."
while read -r cidr; do
  if [[ ! "$cidr" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}/[0-9]{1,2}$ ]]; then
    continue
  fi
  ipset add allowed-domains "$cidr" 2>/dev/null || true
done < <(echo "$gh_ranges" | jq -r '(.web + .api + .git)[]')

# Base domains (always allowed — required for Claude Code + tooling)
ALLOWED_DOMAINS=(
  "registry.npmjs.org"
  "api.anthropic.com"
  "auth.anthropic.com"
  "sentry.io"
  "mcp.sentry.dev"
  "us.sentry.io"
  "o4511139577790464.ingest.us.sentry.io"
  "statsig.anthropic.com"
  "statsig.com"
  "claude.ai"
  "context7.com"
  "playwright.dev"
  "proxy.golang.org"
  "sum.golang.org"
  "storage.googleapis.com"
)
# GitHub Actions blob hosts
for i in $(seq 1 20); do
  ALLOWED_DOMAINS+=("productionresultssa${i}.blob.core.windows.net")
done

# Project-specific domains from environment (space-separated)
if [ -n "${CSPACE_FIREWALL_DOMAINS:-}" ]; then
  IFS=' ' read -ra EXTRA_DOMAINS <<< "$CSPACE_FIREWALL_DOMAINS"
  ALLOWED_DOMAINS+=("${EXTRA_DOMAINS[@]}")
fi

for domain in "${ALLOWED_DOMAINS[@]}"; do
  ips=$(dig +short A "$domain" 2>/dev/null | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' || true)
  if [ -z "$ips" ]; then
    echo "WARNING: Failed to resolve $domain"
    continue
  fi

  while read -r ip; do
    if [[ ! "$ip" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
      continue
    fi
    ipset add allowed-domains "$ip" 2>/dev/null || true
  done < <(echo "$ips")
done

# Vercel CDN ranges
for cidr in 76.76.21.0/24 64.29.17.0/24 216.198.79.0/24; do
  ipset add allowed-domains "$cidr" 2>/dev/null || true
done

# Allow all connected Docker networks
for subnet in $(ip route | awk '/^[0-9]/ && !/default/ {print $1}'); do
  iptables -A INPUT -s "$subnet" -j ACCEPT
  iptables -A OUTPUT -d "$subnet" -j ACCEPT
done

# Allow the default gateway (host)
HOST_IP=$(ip route | grep default | cut -d" " -f3)
if [ -z "$HOST_IP" ]; then
  echo "ERROR: Failed to detect host IP"
  exit 1
fi
HOST_NETWORK=$(echo "$HOST_IP" | sed "s/\.[0-9]*$/.0\/24/")
iptables -A INPUT -s "$HOST_NETWORK" -j ACCEPT
iptables -A OUTPUT -d "$HOST_NETWORK" -j ACCEPT

# Set default policies to DROP
iptables -P INPUT DROP
iptables -P FORWARD DROP
iptables -P OUTPUT DROP

# Allow established connections
iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# Allow only specific outbound traffic
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

# REJECT all other outbound traffic for immediate feedback
iptables -A OUTPUT -j REJECT --reject-with icmp-admin-prohibited

echo "Firewall configuration complete"

# Verify
if curl --connect-timeout 5 https://example.com >/dev/null 2>&1; then
  echo "ERROR: Firewall verification failed - was able to reach https://example.com"
  exit 1
fi

if ! curl --connect-timeout 5 https://api.github.com/zen >/dev/null 2>&1; then
  echo "ERROR: Firewall verification failed - unable to reach https://api.github.com"
  exit 1
fi

echo "Firewall initialized successfully"
touch /tmp/.firewall-init-done
