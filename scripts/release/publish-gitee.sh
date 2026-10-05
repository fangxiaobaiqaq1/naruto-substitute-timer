#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 TAG NOTES_FILE ASSET..." >&2
  exit 2
fi
if [[ -z "${GITEE_TOKEN:-}" ]]; then
  if [[ ! -t 0 || ! -t 1 ]]; then
    echo "GITEE_TOKEN is required when running non-interactively" >&2
    exit 2
  fi
  printf 'Gitee personal access token: ' >&2
  IFS= read -r -s GITEE_TOKEN
  printf '\n' >&2
  [[ -n "$GITEE_TOKEN" ]] || { echo "GITEE_TOKEN cannot be empty" >&2; exit 2; }
  export GITEE_TOKEN
fi

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
    --connect-timeout 10 --max-time 120 \
    --noproxy 'gitee.com,foruda.gitee.com' \
    --header 'Accept: application/json' \
    --header "Authorization: token $GITEE_TOKEN" "$@"
}

download_request() {
  request --location "$@"
}

max_attachment_bytes=100000000
declare -a upload_assets=()
checksum_asset=''
for asset in "$@"; do
  [[ -f "$asset" ]] || { echo "asset not found: $asset" >&2; exit 1; }
  name=$(basename -- "$asset")
  size=$(wc -c < "$asset")
  if (( size > max_attachment_bytes )); then
    case "${name,,}" in
      *.zip)
        echo "Skipping optional ZIP over Gitee 100 MB limit: $name ($size bytes)"
        continue
        ;;
      *)
        echo "Required artifact exceeds Gitee 100 MB limit: $name ($size bytes)" >&2
        exit 1
        ;;
    esac
  fi
  upload_assets+=("$asset")
  [[ "$name" == "SHA256SUMS.txt" ]] && checksum_asset=$asset
done

notes=$(<"$notes_file")
payload=$(jq -n --arg tag "$tag" --arg name "$release_name" --arg body "$notes" \
  '{tag_name:$tag,name:$name,body:$body,prerelease:false} +
   (if (env.GITEE_TARGET_COMMITISH // "") == "" then {} else {target_commitish:env.GITEE_TARGET_COMMITISH} end)')

release_list=$(request "$api/releases?per_page=100&direction=desc")
release_id=$(jq -r --arg tag "$tag" \
  '.[] | select((.tag_name // .tag) == $tag) | .id // empty' <<<"$release_list" | head -n 1)
release_exists=0
if [[ -z "$release_id" ]]; then
  release_body=$(request --header 'Content-Type: application/json' \
    --data "$payload" "$api/releases")
  release_id=$(jq -r '.id // empty' <<<"$release_body")
else
  release_exists=1
fi
[[ "$release_id" =~ ^[0-9]+$ ]] || { echo "Gitee response did not contain a release id" >&2; exit 1; }

attachments=$(request "$api/releases/$release_id/attach_files?per_page=100")
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

attachment_id() {
  jq -r --arg name "$1" '
    [.[] | select(.name == $name) | .id] |
    if length == 0 then "" elif length == 1 then .[0] else "duplicate" end' <<<"$attachments"
}

checksum_existing_id=''
checksum_changed=0
for asset in "${upload_assets[@]}"; do
  name=$(basename -- "$asset")
  existing_id=$(attachment_id "$name")
  [[ "$existing_id" != "duplicate" ]] || { echo "Gitee has duplicate attachments named $name" >&2; exit 1; }
  [[ -z "$existing_id" || "$existing_id" =~ ^[0-9]+$ ]] || { echo "Gitee attachment id is invalid for $name" >&2; exit 1; }

  if [[ "$name" == "SHA256SUMS.txt" ]]; then
    checksum_existing_id=$existing_id
    if [[ -n "$existing_id" ]]; then
      remote_file="$tmpdir/$name.remote"
      download_request --output "$remote_file" "$api/releases/$release_id/attach_files/$existing_id/download"
      if cmp -s "$asset" "$remote_file"; then
        echo "Gitee attachment already matches local checksum: $name"
      else
        checksum_changed=1
      fi
    fi
    continue
  fi

  case "${name,,}" in
    *.exe|*.apk)
      if [[ -n "$existing_id" ]]; then
        remote_file="$tmpdir/$name.remote"
        download_request --output "$remote_file" "$api/releases/$release_id/attach_files/$existing_id/download"
        local_sha=$(sha256sum "$asset" | awk '{print $1}')
        remote_sha=$(sha256sum "$remote_file" | awk '{print $1}')
        if [[ "$local_sha" != "$remote_sha" ]]; then
          echo "Gitee attachment differs from local artifact; refusing to replace: $name" >&2
          exit 1
        fi
        echo "Gitee attachment already matches local SHA256: $name"
      fi
      ;;
    *)
      [[ -n "$existing_id" ]] && echo "Gitee attachment already exists: $name"
      ;;
  esac
done

if (( release_exists )); then
  update_payload=$(jq -n --arg tag "$tag" --arg name "$release_name" --arg body "$notes" \
    '{tag_name:$tag,name:$name,body:$body,prerelease:false}')
  request --request PATCH --header 'Content-Type: application/json' \
    --data "$update_payload" "$api/releases/$release_id" >/dev/null
  echo "Updated Gitee release notes: $tag"
fi

for asset in "${upload_assets[@]}"; do
  name=$(basename -- "$asset")
  [[ "$name" != "SHA256SUMS.txt" ]] || continue
  existing_id=$(attachment_id "$name")
  [[ -n "$existing_id" ]] && continue
  echo "Uploading $name to Gitee release $tag"
  request --form "file=@$asset" "$api/releases/$release_id/attach_files" >/dev/null
done

if [[ -n "$checksum_asset" ]]; then
  if [[ -n "$checksum_existing_id" && "$checksum_changed" -eq 1 ]]; then
    echo "Replacing changed Gitee checksum attachment: SHA256SUMS.txt"
    request --request DELETE \
      "$api/releases/$release_id/attach_files/$checksum_existing_id" >/dev/null
    checksum_existing_id=''
  fi
  if [[ -z "$checksum_existing_id" ]]; then
    echo "Uploading SHA256SUMS.txt to Gitee release $tag"
    request --form "file=@$checksum_asset" "$api/releases/$release_id/attach_files" >/dev/null
  fi
fi

echo "Gitee release published: https://gitee.com/$repository/releases/tag/$tag"
