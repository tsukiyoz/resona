#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
version=${VERSION:-v$(cat "$repo_dir/VERSION")}
arch=${DEPLOY_ARCH:-amd64}
build_time=${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
case "$version" in *[!A-Za-z0-9._+-]*) echo 'Invalid VERSION' >&2; exit 1 ;; esac
case "$build_time" in *[!A-Za-z0-9.:+-]*) echo 'Invalid BUILD_TIME' >&2; exit 1 ;; esac
case "$arch" in amd64|arm64) ;; *) echo 'DEPLOY_ARCH must be amd64 or arm64' >&2; exit 1 ;; esac

base="$repo_dir/build/deploy"
name="resona-server-$version-linux-$arch"
output="$base/$name"
mkdir -p "$base"
lock="$base/.$name.lock"
if ! mkdir "$lock"; then echo 'This deployment package is already being generated' >&2; exit 1; fi
stage=
cleanup() { if [ -n "$stage" ]; then rm -rf "$stage"; fi; rmdir "$lock"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
if [ -e "$output" ] && { [ -L "$output" ] || [ ! -f "$output/.resona-deploy-package" ]; }; then
  echo "Refusing to replace an unrecognized deployment directory: $output" >&2
  exit 1
fi
stage=$(mktemp -d "$base/.package.XXXXXX")
mkdir "$stage/$name"
package="$stage/$name"
flags="-s -w -X github.com/tsukiyoz/resona/internal/version.Version=$version -X github.com/tsukiyoz/resona/internal/version.BuildTime=$build_time"
(cd "$repo_dir" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -mod=readonly -trimpath \
  -ldflags "$flags" -o "$package/resona-server" ./cmd/resona-server)
cp "$repo_dir/deploy/Dockerfile.prebuilt" "$package/Dockerfile"
cp "$repo_dir/deploy/prebuilt.dockerignore" "$package/.dockerignore"
cp "$repo_dir/deploy/activate.sh" "$package/activate.sh"
cp "$repo_dir/deploy/README.md" "$package/README.md"
cp "$repo_dir/docs/noise-license.txt" "$package/noise-license.txt"
chmod 755 "$package/activate.sh" "$package/resona-server"
touch "$package/.resona-deploy-package"
go version -m "$package/resona-server" > "$package/BUILD_INFO.txt"
tar -C "$stage" -czf "$stage/$name.tar.gz" "$name"

# Only complete generated packages replace previous generated output.
rm -rf "$output"
mv "$package" "$output"
mv -f "$stage/$name.tar.gz" "$base/$name.tar.gz"
echo "Package: $output"
echo "Archive: $base/$name.tar.gz"
