#!/usr/bin/env bash
# Build locally; publish only after the developer has reviewed the notes.
set -euo pipefail
cd "$(dirname "$0")/.."
dry_run=false
if [[ ${1:-} == --dry-run ]]; then dry_run=true; shift; fi
version=${1:-}
if [[ ! $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ || $# != 1 ]]; then
    echo 'Usage: scripts/release.sh [--dry-run] VERSION' >&2
    exit 2
fi
if [[ $(cat VERSION) != "$version" ]]; then
    echo 'Update VERSION, commit it, and rerun with that version.' >&2
    exit 1
fi
if [[ -n $(git status --porcelain) ]]; then
    echo 'Commit or remove untracked changes before releasing.' >&2
    exit 1
fi
tag="v$version"
if git rev-parse "$tag" >/dev/null 2>&1 && [[ $(git rev-list -n 1 "$tag") != "$(git rev-parse HEAD)" ]]; then
    echo "$tag does not point to HEAD; check out the release commit." >&2
    exit 1
fi
case "$(uname -s)-$(uname -m)" in
    Darwin-arm64) platform=macos-arm64; extension=zip ;;
    Linux-x86_64) platform=linux-x86_64; extension=tar.gz ;;
    *) echo 'Supported hosts: macOS arm64 and Linux x86_64.' >&2; exit 1 ;;
esac
out="$PWD/build/release/$version/$platform"
mkdir -p "$out"
asset="wisp-$version-$platform.$extension"
rm -f "$out/$asset"
# The caller can place its memory-capped build wrapper in RELEASE_BUILD_WRAPPER.
if [[ -n ${RELEASE_BUILD_WRAPPER:-} ]]; then
    "$RELEASE_BUILD_WRAPPER" make build
else
    make build
fi
if [[ $platform == macos-arm64 ]]; then
    app="$out/Wisp.app"
    rm -rf "$app"
    ditto build/ui/wisp.app "$app"
    cp bin/wispd bin/wispctl "$app/Contents/MacOS/"
    # Optional assets from the developer's local WhatsApp asset cache.
    for name in Wisp.icns notification.mp3 notification.ogg; do
        if [[ -f build/whatsapp/$name ]]; then cp "build/whatsapp/$name" "$app/Contents/Resources/"; fi
    done
    deploy_args=("$app" "-qmldir=$PWD/ui/qml" -always-overwrite)
    qt_libdir=${QT_LIBDIR:-/opt/homebrew/lib}
    if [[ -d $qt_libdir ]]; then
        deploy_args+=("-libpath=$qt_libdir")
        # macdeployqt propagates used runpaths to its plugin resolver. Its
        # -libpath option alone does not reach that resolver on split Qt installs.
        binary="$app/Contents/MacOS/wisp"
        core=$(otool -L "$binary" | sed -n 's/^[[:space:]]*\(.*QtCore.framework\/Versions\/A\/QtCore\) (compatibility.*/\1/p')
        if [[ -n $core && -f $qt_libdir/QtCore.framework/Versions/A/QtCore ]]; then
            if ! otool -l "$binary" | grep -Fq "path $qt_libdir ("; then
                install_name_tool -add_rpath "$qt_libdir" "$binary"
            fi
            install_name_tool -change "$core" '@rpath/QtCore.framework/Versions/A/QtCore' "$binary"
        fi
    fi
    "${MACDEPLOYQT:-macdeployqt}" "${deploy_args[@]}" 2>&1 | tee "$out/deployment.log"
    # macdeployqt can return success even when it could not copy a dependency.
    if grep -q '^ERROR:' "$out/deployment.log"; then
        echo 'Qt deployment failed; inspect deployment.log before releasing.' >&2
        exit 1
    fi
    codesign --force --deep --sign - "$app"
    codesign --verify --deep "$app"
    ditto -c -k --sequesterRsrc --keepParent "$app" "$out/$asset"
else
    tar -czf "$out/$asset" -C bin wispd wispctl -C "$PWD/build/ui" wisp
fi
previous=$(git describe --tags --match 'v[0-9]*' --abbrev=0 HEAD^ 2>/dev/null || true)
range=HEAD
if [[ -n $previous ]]; then range="$previous..HEAD"; fi
notes="$out/notes.md"
if [[ ! -f $notes ]]; then git log --format='- %s' "$range" > "$notes"; fi
if $dry_run; then
    (cd "$out" && shasum -a 256 "$asset") > "$out/SHA256SUMS.local"
    echo "Dry run: built $out/$asset"
    echo "Release notes: $notes"
    echo "Would tag HEAD as $tag and upload $asset with a merged SHA256SUMS. No tag or release changed."
    exit 0
fi
command -v gh >/dev/null
# A separate host must fetch and check out exactly the same release commit.
remote_tag=$(git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}")
if [[ -n $remote_tag ]]; then
    remote_commit=$(printf '%s\n' "$remote_tag" | awk 'END {print $1}')
    if [[ $remote_commit != "$(git rev-parse HEAD)" ]]; then
        echo 'Remote tag differs from HEAD; fetch and check out the release tag.' >&2
        exit 1
    fi
fi
if [[ -n ${EDITOR:-} ]]; then "$EDITOR" "$notes"; fi
printf 'Publish %s from %s? [y/N] ' "$tag" "$(git rev-parse --short HEAD)"
read -r answer
[[ $answer == y || $answer == Y ]] || exit 1
if ! git rev-parse "$tag" >/dev/null 2>&1; then git tag "$tag"; fi
git push origin "refs/tags/$tag"
sums="$out/SHA256SUMS"
touch "$sums"
if gh release view "$tag" --repo aShanki/wisp >/dev/null 2>&1; then
    # Distinguish a missing checksum from a failed download; never discard an existing manifest.
    assets=$(gh release view "$tag" --repo aShanki/wisp --json assets --jq '.assets[].name')
    if grep -qx SHA256SUMS <<< "$assets"; then
        gh release download "$tag" --repo aShanki/wisp --pattern SHA256SUMS --output "$sums" --clobber
    fi
else
    gh release create "$tag" --repo aShanki/wisp --verify-tag --title "Wisp $version" --notes-file "$notes"
fi
awk -v asset="$asset" '$2 != asset {print}' "$sums" > "$sums.next"
(cd "$out" && shasum -a 256 "$asset") >> "$sums.next"
mv "$sums.next" "$sums"
gh release upload "$tag" "$out/$asset" "$sums" --repo aShanki/wisp --clobber
