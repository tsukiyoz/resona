#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_REPOSITORY:?}"
: "${GITHUB_SHA:?}"
: "${RELEASE_TAG:?}"
: "${RESONA_UPDATE_PUBLIC_KEY:?}"
: "${RESONA_UPDATE_SIGNING_KEY:?}"
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
test "$RELEASE_TAG" = "v$(tr -d '\r\n' < VERSION)"
notes="docs/releases/$RELEASE_TAG.md"
test -s "$notes"

verify_tag() {
  git fetch --quiet --no-tags origin "+refs/tags/$RELEASE_TAG:refs/resona-release/tag"
  test "$(git cat-file -t refs/resona-release/tag)" = tag
  test "$(git rev-parse 'refs/resona-release/tag^{commit}')" = "$GITHUB_SHA"
}
verify_tag

# Read the whole release list rather than interpreting any API error as absence.
releases=$(gh api --paginate "repos/$GITHUB_REPOSITORY/releases?per_page=100" --jq '.[] | {tag_name, draft, prerelease}')
if printf '%s\n' "$releases" | jq -se --arg tag "$RELEASE_TAG" 'any(.[]; .tag_name == $tag)' >/dev/null; then
  echo 'Release already exists; published versions are immutable and drafts require maintainer review.' >&2
  exit 1
fi
latest=true
while IFS= read -r published; do
  [[ "$published" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue
  newest=$(printf '%s\n%s\n' "${published#v}" "${RELEASE_TAG#v}" | sort -V | tail -n 1)
  if [[ "$newest" != "${RELEASE_TAG#v}" ]]; then latest=false; fi
done < <(printf '%s\n' "$releases" | jq -sr '.[] | select(.draft == false and .prerelease == false) | .tag_name')

output=build/release/publish
mkdir -p "$output"
cp build/release/windows/Resona-win-x64.zip "$output/Resona-$RELEASE_TAG-win-x64-portable.zip"
cp build/release/arm64/Resona-macos-arm64.zip "$output/Resona-$RELEASE_TAG-macos-arm64-portable.zip"
cp build/release/x64/Resona-macos-x64.zip "$output/Resona-$RELEASE_TAG-macos-x64-portable.zip"
jq -n --arg dir "$output" --arg tag "$RELEASE_TAG" --arg repo "$GITHUB_REPOSITORY" '
  [{platform:"windows/amd64",suffix:"win-x64"},
   {platform:"darwin/arm64",suffix:"macos-arm64"},
   {platform:"darwin/amd64",suffix:"macos-x64"}] |
  map(. as $asset | ("Resona-"+$tag+"-"+$asset.suffix+"-portable.zip") as $name |
    {platform:$asset.platform,path:($dir+"/"+$name),
     url:("https://github.com/"+$repo+"/releases/download/"+$tag+"/"+$name)})
' > "$output/assets.json"
expires=$(date -u -d '+365 days' '+%Y-%m-%dT%H:%M:%SZ')
go run ./tools/release-manifest -version "$RELEASE_TAG" -assets "$output/assets.json" \
  -notes "$notes" -expires "$expires" -public-key "$RESONA_UPDATE_PUBLIC_KEY" -out "$output/manifest.json"

# A failed upload remains a draft and cannot be discovered through /latest.
verify_tag
gh release create "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --verify-tag --draft \
  --title "Resona $RELEASE_TAG" --notes-file "$notes"
gh release upload "$RELEASE_TAG" "$output/Resona-$RELEASE_TAG-win-x64-portable.zip" \
  "$output/Resona-$RELEASE_TAG-macos-arm64-portable.zip" "$output/Resona-$RELEASE_TAG-macos-x64-portable.zip" \
  "$output/manifest.json" --repo "$GITHUB_REPOSITORY"
verify_tag
gh release edit "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --draft=false --latest="$latest"
