#!/bin/sh
set -eu
: "${TLA_JAR:?Set TLA_JAR to the absolute path of tla2tools.jar (release v1.7.4)}"
extension_case=${1:?Supply a configuration name; see README.md}
case "$extension_case" in
    replay|replay-first-corrupt|metadata-conflict|conflict-stability) extension_module=Conflicts ;;
    limits-unbounded|limits-reject|crash-volatile|crash-durable) extension_module=Storage ;;
    capture-snapshot|capture-late|boot-stale) extension_module=Capture ;;
    concurrent-unlocked|concurrent-serialized) extension_module=Concurrent ;;
    *) echo "unknown extension configuration: $extension_case" >&2; exit 2 ;;
esac
cd "$(dirname "$0")"
extension_states=$(mktemp -d "${TMPDIR:-/tmp}/pid-attribution-extension.XXXXXX")
trap 'rm -rf "$extension_states"' 0
java -XX:+UseParallelGC -Xmx1g -cp "$TLA_JAR" tlc2.TLC \
    -workers 1 -fp 0 -seed 1 -metadir "$extension_states" \
    -config "$extension_case.cfg" "$extension_module.tla"
