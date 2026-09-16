#!/usr/bin/env bash
set -euo pipefail
build="$(pwd)/build/opuscodec"
binary="$build/package/opus-bench"
if [[ -f "$binary.exe" ]]; then binary="$binary.exe"; fi
failed=0
for mode in cbr vbr; do
    cbr=true
    if [[ "$mode" == vbr ]]; then cbr=false; fi
    status=0
    "$binary" -seconds 15 -repeats 5 -cbr="$cbr" -out "$build/reports/$mode" > "$build/reports/$mode.log" 2>&1 || status=$?
    cat "$build/reports/$mode.log"
    printf '%s exit status: %s\n' "$mode" "$status" >> "$build/reports/status.txt"
    if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
        printf '\n## %s (exit %s)\n\n' "$mode" "$status" >> "$GITHUB_STEP_SUMMARY"
        if [[ -f "$build/reports/$mode/report.md" ]]; then
            cat "$build/reports/$mode/report.md" >> "$GITHUB_STEP_SUMMARY"
        fi
    fi
    if [[ "$status" != 0 ]]; then failed=1; fi
done
exit "$failed"
