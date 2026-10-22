#!/usr/bin/env bash
set -euo pipefail
umask 077

if [[ $# -ne 2 ]]; then
    printf 'Usage: bash deploy/install-user.sh BINARY RELEASE\n' >&2
    exit 2
fi
if [[ $(id -u) -eq 0 ]]; then
    printf 'Run this installer as the service user, without sudo.\n' >&2
    exit 2
fi

binary=$(realpath -- "$1")
release=$2
if [[ ! $release =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]]; then
    printf 'Invalid release name.\n' >&2
    exit 2
fi
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
app_root="${HOME:?}/.local/share/printbridge"
config_dir="$HOME/.config/printbridge"
unit_dir="$HOME/.config/systemd/user"
unit_source="$script_dir/systemd/printbridge.service"
unit_target="$unit_dir/printbridge.service"
release_dir="$app_root/releases/$release"

for tool in systemctl systemd-analyze curl pdfinfo pdftoppm; do
    command -v "$tool" >/dev/null || {
        printf 'Missing required command: %s\n' "$tool" >&2
        exit 1
    }
done
systemctl --user show-environment >/dev/null
"$binary" -version

# Re-running the same release is safe; upgrades need a stopped, backed-up service.
if [[ -e "$app_root/current" || -L "$app_root/current" ]]; then
    if [[ ! -L "$app_root/current" ]] ||
        [[ $(readlink -f -- "$app_root/current") != "$release_dir" ]]; then
        printf 'Another release is installed. Follow docs/deployment.md to upgrade.\n' >&2
        exit 1
    fi
fi
if [[ -e "$release_dir/printbridge" ]] && ! cmp -s "$binary" "$release_dir/printbridge"; then
    printf 'This release name already contains a different binary.\n' >&2
    exit 1
fi
if [[ -e "$unit_target" ]] && ! cmp -s "$unit_source" "$unit_target"; then
    printf 'An existing service unit differs; preserve it and review it before installing.\n' >&2
    exit 1
fi
if [[ ! -e "$config_dir/service.env" ]]; then
    if curl --noproxy '*' --silent --connect-timeout 2 --max-time 2 \
        http://127.0.0.1:8765/healthz >/dev/null; then
        printf 'Port 8765 is already in use. Configure a free port before installing.\n' >&2
        exit 1
    fi
fi

install -d -m 700 "$app_root" "$app_root/releases" "$release_dir" \
    "$app_root/data" "$config_dir"
mkdir -p -- "$unit_dir"
if [[ ! -e "$release_dir/printbridge" ]]; then
    install -m 700 "$binary" "$release_dir/printbridge"
fi
if [[ ! -e "$config_dir/service.env" ]]; then
    pairing_code=$("$release_dir/printbridge" -new-pairing-code)
    {
        printf 'PRINTBRIDGE_LISTEN_ADDR=0.0.0.0:8765\n'
        printf 'PRINTBRIDGE_CUPS_URL=http://localhost:631\n'
        printf 'PRINTBRIDGE_PAIRING_CODE=%s\n' "$pairing_code"
        printf 'PRINTBRIDGE_PDFINFO=/usr/bin/pdfinfo\n'
        printf 'PRINTBRIDGE_PDFTOPPM=/usr/bin/pdftoppm\n'
        printf 'PRINTBRIDGE_OFFICE_COMMAND=/usr/bin/soffice\n'
    } > "$config_dir/service.env"
    unset pairing_code
fi
chmod 600 "$config_dir/service.env"
if [[ ! -L "$app_root/current" ]]; then
    ln -s -- "releases/$release" "$app_root/current"
fi
install -m 600 "$unit_source" "$unit_target"
systemd-analyze --user verify "$unit_target"
systemctl --user daemon-reload
systemctl --user enable --now printbridge.service
systemctl --user is-active --quiet printbridge.service

printf 'Service installed: %s\n' "$release_dir"
printf 'Private configuration: %s/service.env\n' "$config_dir"
printf 'Status: systemctl --user status printbridge.service\n'
if [[ $(loginctl show-user "$(id -un)" -p Linger --value) != yes ]]; then
    printf 'Boot startup needs an administrator: sudo loginctl enable-linger %s\n' "$(id -un)"
fi
if ! command -v soffice >/dev/null; then
    printf 'Office conversion is pending: sudo bash %s/install-dependencies.sh\n' "$script_dir"
fi
