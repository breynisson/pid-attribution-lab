#!/bin/sh
set -eu

: "${TLA_JAR:?Set TLA_JAR to the absolute path of tla2tools.jar (release v1.7.4)}"
tlc_mode=${1:-corrected}
case "$tlc_mode" in
    faulty|corrected|scopes|unfair|fair) ;;
    *) echo "usage: sh formal/run.sh [faulty|corrected|scopes|unfair|fair]" >&2; exit 2 ;;
esac
cd "$(dirname "$0")"
tlc_states=$(mktemp -d "${TMPDIR:-/tmp}/pid-attribution-tlc.XXXXXX")
trap 'rm -rf "$tlc_states"' 0
java -XX:+UseParallelGC -Xmx1g -cp "$TLA_JAR" tlc2.TLC \
    -workers 1 -fp 0 -seed 1 -metadir "$tlc_states" \
    -config "$tlc_mode.cfg" Attribution.tla
