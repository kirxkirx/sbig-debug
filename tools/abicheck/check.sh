#!/bin/sh
# Run from anywhere. Optional SBIG_SDK_ARCHIVE avoids downloading an existing kit.
set -eu
cd "$(dirname "$0")/../.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
archive=${SBIG_SDK_ARCHIVE:-"$work/WinDevKit.zip"}
if [ -z "${SBIG_SDK_ARCHIVE:-}" ]; then
    curl --fail --location --retry 3 --connect-timeout 20 --max-time 180 \
        https://diffractionlimited.com/downloads/sbig/WinDevKit.zip -o "$archive"
fi
# Pin the exact vendor kit and header used to validate our Windows ABI. A vendor
# update intentionally fails rather than silently changing our reference.
printf '%s  %s\n' d411de3c1781ccaed3765e14717b80719029f6fc08a04bcae876fdd8e32c6cdc "$archive" | sha256sum -c -
unzip -p "$archive" sbigudrv.h > "$work/sbigudrv.h"
printf '%s  %s\n' 7d865ad7549ffb9d489f5b64cf84e473ef55238a743e9b2a009388e38fad3cee "$work/sbigudrv.h" | sha256sum -c -
SBIG_ABI_CHECK_OUT="$work/check.c" go test -run '^TestGenerateABICheck$' -count=1 ./internal/sbig
for target in i686-pc-windows-msvc x86_64-pc-windows-msvc; do
    clang -target "$target" -fsyntax-only -ffreestanding -fms-extensions \
        -Wno-pragma-pack -I tools/abicheck/stub -I "$work" "$work/check.c"
    echo "Vendor ABI verified: $target"
done
