#!/bin/sh
# Runs from a private staging directory after the UI has handed off the update.
set -eu
root=$1
target=$2
exe=$3
pid=$4
changed=$5
daemon_pid=$6
exec >>"$root/install.log" 2>&1
while kill -0 "$pid" 2>/dev/null; do sleep 1; done
restart() {
    if [ "$(uname -s)" = Darwin ]; then
        job="gui/$(id -u)/io.github.aShanki.wisp"
        if launchctl print "$job" 2>/dev/null | grep -Fq "$target/Contents/MacOS/wisp"; then
            launchctl kickstart -k "$job" || open "$target"
        else
            open "$target"
        fi
    else
        if [ "$changed" = 1 ]; then systemctl --user restart wispd.service || true; fi
        "$exe" &
    fi
}
if [ "$changed" = 1 ] && [ "$daemon_pid" -gt 0 ]; then
    attempts=0
    while kill -0 "$daemon_pid" 2>/dev/null; do
        attempts=$((attempts + 1))
        if [ "$attempts" -ge 30 ]; then
            echo 'Daemon did not stop; update cancelled'
            restart
            exit 1
        fi
        sleep 1
    done
fi
if [ "$(uname -s)" = Darwin ]; then
    if ! mv "$target" "$root/old.app"; then restart; exit 1; fi
    if ! mv "$root/new/Wisp.app" "$target"; then
        mv "$root/old.app" "$target"
        restart
        exit 1
    fi
    xattr -dr com.apple.quarantine "$target" || true
else
    mkdir "$root/old"
    for name in wisp wispd wispctl; do
        if ! cp -p "$target/$name" "$root/old/$name"; then restart; exit 1; fi
    done
    for name in wisp wispd wispctl; do
        if ! mv "$root/new/$name" "$target/$name"; then
            for old in wisp wispd wispctl; do mv "$root/old/$old" "$target/$old"; done
            restart
            exit 1
        fi
    done
fi
restart
rm -rf "$root"
