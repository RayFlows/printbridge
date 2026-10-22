#!/usr/bin/env bash
set -euo pipefail

if [[ $(id -u) -ne 0 ]]; then
    printf 'Installing system packages requires: sudo bash deploy/install-dependencies.sh\n' >&2
    exit 1
fi
command -v apt-get >/dev/null || {
    printf 'This dependency installer supports Debian and Raspberry Pi OS.\n' >&2
    exit 1
}

packages=(
    poppler-utils
    libreoffice-writer
    libreoffice-calc
    libreoffice-impress
    fonts-noto-cjk
    fonts-liberation
)
apt-get update
plan=$(LC_ALL=C apt-get --simulate --no-install-recommends --no-upgrade --no-remove \
    install "${packages[@]}")
printf '%s\n' "$plan"
if grep -E '^(Inst|Remv) (cups[^ ]*|libcups[^ ]*|printer-driver-[^ ]*|gutenprint[^ ]*) ' <<< "$plan" >/dev/null; then
    printf 'Dependency resolution would change printing packages; installation stopped.\n' >&2
    exit 1
fi
DEBIAN_FRONTEND=noninteractive apt-get --yes --no-install-recommends --no-upgrade --no-remove \
    install "${packages[@]}"
fc-cache -f
printf 'Document tools installed. Existing CUPS queues and drivers are not configured by this script.\n'
