#!/bin/sh
set -eu
: "${TLA_JAR:?Set TLA_JAR to the absolute path of tla2tools.jar (release v1.7.4)}"
extension_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
extension_log=$(mktemp "${TMPDIR:-/tmp}/pid-attribution-extension-check.XXXXXX")
trap 'rm -f "$extension_log"' 0

check_case() {
    if sh "$extension_dir/run.sh" "$1" > "$extension_log" 2>&1; then
        extension_status=0
    else
        extension_status=$?
    fi
    if [ "$extension_status" -ne "$2" ] || ! grep -Fq "$3" "$extension_log"; then
        echo "FAIL $1: expected exit $2 and '$3'; got exit $extension_status" >&2
        cat "$extension_log" >&2
        return 1
    fi
    printf 'PASS %s: expected exit %s; %s\n' "$1" "$2" "$3"
    sed -n '/states generated,.*distinct states found/p' "$extension_log"
}

# A parser/setup error or the wrong invariant failure cannot count as success.
check_case replay 0 'Model checking completed. No error has been found.'
check_case replay-first-corrupt 12 'Invariant CaptureTruth is violated.'
check_case metadata-conflict 12 'Invariant CaptureTruth is violated.'
check_case conflict-stability 0 'Model checking completed. No error has been found.'
check_case limits-unbounded 12 'Invariant Capacity is violated.'
check_case limits-reject 0 'Model checking completed. No error has been found.'
check_case crash-volatile 12 'Invariant AcceptedRetained is violated.'
check_case crash-durable 0 'Model checking completed. No error has been found.'
check_case capture-late 12 'Invariant CaptureIdentity is violated.'
check_case boot-stale 12 'Invariant CaptureIdentity is violated.'
check_case capture-snapshot 0 'Model checking completed. No error has been found.'
check_case concurrent-unlocked 12 'Invariant QuiescentUseful is violated.'
check_case concurrent-serialized 0 'Model checking completed. No error has been found.'
