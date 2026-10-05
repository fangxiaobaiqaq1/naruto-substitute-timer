#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 TAG NOTES_FILE ASSET..." >&2
  exit 2
fi
: "${GITEE_TOKEN:?set GITEE_TOKEN to a Gitee personal access token}"

tag=$1
notes_file=$2
shift 2
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "invalid tag: $tag" >&2; exit 2; }
[[ -f "$notes_file" ]] || { echo "release notes not found: $notes_file" >&2; exit 2; }
[[ $# -gt 0 ]] || { echo "at least one release asset is required" >&2; exit 2; }

repository=${GITEE_REPOSITORY:-xiaobaiqaq/naruto-substitute-timer}
api="https://gitee.com/api/v5/repos/$repository"
release_name=${GITEE_RELEASE_NAME:-"替身计时器 $tag"}

request() {
  curl --fail-with-body --silent --show-error --retry 3 --retry-delay 2 \
    --url-query "access_token=$GITEE_TOKEN" "$@"
}

notes=$(cat "$notes_file")
payload=$(jq -n --arg tag "$tag" --arg name "$release_name" --arg body "$notes" \
  '{tag_name:$tag,name:$name,body:$body,prerelease:false} +
   (if (env.GITEE_TARGET_COMMITISH // "") == "" then {} else {target_commitish:env.GITEE_TARGET_COMMITISH} end)')

release_list=$(request --header 'Accept: application/json' \
  --header "Authorization: token $GITEE_TOKEN" \
  "$api/releases?per_page=100&direction=desc")
release_id=$(jq -r --arg tag "$tag" \
  '.[] | select((.tag_name // .tag) == $tag) | .id // empty' <<<"$release_list" | head -n 1)
if [[ -z "$release_id" ]]; then
  release_body=$(request --header 'Accept: application/json' \
    --header "Authorization: token $GITEE_TOKEN" \
    --header 'Content-Type: application/json' \
    --data "$payload" "$api/releases")
  release_id=$(jq -r '.id // empty' <<<"$release_body")
fi
[[ "$release_id" =~ ^[0-9]+$ ]] || { echo "Gitee response did not contain a release id" >&2; exit 1; }

attachments=$(request --header 'Accept: application/json' \
  --header "Authorization: token $GITEE_TOKEN" \
  "$api/releases/$release_id/attach_files?per_page=100")

for asset in "$@"; do
  [[ -f "$asset" ]] || { echo "asset not found: $asset" >&2; exit 1; }
  name=$(basename "$asset")
  if jq -e --arg name "$name" '.[] | select(.name == $name)' <<<"$attachments" >/dev/null; then
    echo "Gitee attachment already exists: $name"
    continue
  fi
  echo "Uploading $name to Gitee release $tag"
  request --header 'Accept: application/json' \
    --header "Authorization: token $GITEE_TOKEN" \
    --form "file=@$asset" "$api/releases/$release_id/attach_files" >/dev/null
done

echo "Gitee release published: https://gitee.com/$repository/releases/tag/$tag"
