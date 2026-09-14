#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
output=${1:-"build/bin/broadcaster/$(date +%Y%m%d-%H%M%S)"}
mkdir -p "$output"
output=$(cd "$output" && pwd)
go test -c -o "$output/experiment.test" ./test/broadcaster
BROADCAST_EXPERIMENT=1 BROADCAST_OUTPUT="$output" \
    "$output/experiment.test" -test.run='^TestArchitectureExperiment$' -test.v -test.timeout=15m
node test/broadcaster/summarize.mjs "$output"
