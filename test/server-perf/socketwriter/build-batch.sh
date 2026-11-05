#!/bin/sh
set -eu

output=${1:-build/server-writer/batch}
mkdir -p "$output"
output=$(cd "$output" && pwd)
if [ -e "$output/experiment.mod" ]; then
    echo "Use a fresh output directory: $output" >&2
    exit 1
fi
cp go.mod "$output/experiment.mod"
cp go.sum "$output/experiment.sum"
# Test-only dependency; the product module and lockfile remain unchanged.
go mod edit -modfile "$output/experiment.mod" -require=golang.org/x/net@v0.56.0
for model in baseline writer batch; do
    overlay="$output/overlay-$model"
    go run test/server-perf/socketwriter/prepare.go -output "$overlay" -model "$model" -slots 16
    CGO_ENABLED=0 GOOS=linux GOARCH=${RESONA_BENCH_GOARCH:-arm64} go test \
        -modfile "$output/experiment.mod" -mod=mod -overlay "$overlay/overlay.json" \
        -c -o "$output/$model.test" ./internal/server
done
CGO_ENABLED=0 GOOS=linux GOARCH=${RESONA_BENCH_GOARCH:-arm64} go test \
    -modfile "$output/experiment.mod" -mod=mod -overlay "$output/overlay-batch/overlay.json" \
    -c -o "$output/batch-unit.test" ./internal/noiseudp
cp test/server-perf/socketwriter/measure-batch.sh "$output/measure.sh"
