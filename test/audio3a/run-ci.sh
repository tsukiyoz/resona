#!/usr/bin/env bash
set -euo pipefail
build="$(pwd)/build/audio3a"
suffix=
if [[ -f "$build/package/resona-3a-bench.exe" ]]; then suffix=.exe; fi
failed=0
for model in regular little; do
    name=resona-3a-bench
    options=(--seconds 6 --warmup 3 --repeats 3 --wavs)
    if [[ "$model" == little ]]; then
        name=resona-3a-bench-little
        options+=(--aec off --ns rnnoise --agc off)
    fi
    status=0
    "$build/package/$name$suffix" \
        --out "$build/reports/$model" "${options[@]}" > "$build/reports/$model.log" 2>&1 || status=$?
    cat "$build/reports/$model.log"
    printf '%s exit status: %s\n' "$model" "$status" >> "$build/reports/status.txt"
    if [[ -n "${GITHUB_STEP_SUMMARY:-}" && -f "$build/reports/$model/report.md" ]]; then
        printf '\n## RNNoise model: %s (exit %s)\n\n' "$model" "$status" >> "$GITHUB_STEP_SUMMARY"
        cat "$build/reports/$model/report.md" >> "$GITHUB_STEP_SUMMARY"
    fi
    if [[ "$status" != 0 ]]; then failed=1; fi
done
exit "$failed"
