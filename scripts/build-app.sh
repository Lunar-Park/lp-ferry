#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
make build
swift build -c release --package-path app --product Ferry
release=$(swift build -c release --package-path app --show-bin-path)
mkdir -p dist
stage=$(mktemp -d dist/.ferry-build.XXXXXX)
trap 'rm -rf "$stage"' EXIT
bundle="$stage/Ferry.app/Contents"
mkdir -p "$bundle/MacOS" "$bundle/Resources/bin"
install -m 755 "$release/Ferry" "$bundle/MacOS/Ferry"
install -m 755 bin/ferry "$bundle/Resources/bin/ferry"
cp app/Sources/Ferry/Resources/* "$bundle/Resources/"
cp assets/AppIcon.icns "$bundle/Resources/"
cp packaging/Ferry-Info.plist "$bundle/Info.plist"
identity=${FERRY_SIGN_IDENTITY:--}
codesign --force --sign "$identity" "$bundle/Resources/bin/ferry"
codesign --force --sign "$identity" "$stage/Ferry.app"
codesign --verify --deep --strict "$stage/Ferry.app"
if [[ -e dist/Ferry.app ]]; then mv dist/Ferry.app "$stage/previous.app"; fi
if ! mv "$stage/Ferry.app" dist/Ferry.app; then
    if [[ -e "$stage/previous.app" ]]; then mv "$stage/previous.app" dist/Ferry.app; fi
    exit 1
fi
echo "Built dist/Ferry.app"
