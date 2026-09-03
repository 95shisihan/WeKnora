#!/bin/sh
set -eu

profile="weknora-plugin-no-network"
image="weknora/plugin-network-probe:local"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)

if ! aa-status 2>/dev/null | grep -q "$profile"; then
  echo "AppArmor profile $profile is not loaded" >&2
  echo "Run: sudo apparmor_parser -r $repo_root/deploy/apparmor/$profile" >&2
  exit 1
fi

docker build -t "$image" "$script_dir"
started=$(date --iso-8601=seconds)
docker run --rm \
  --network none \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --security-opt "apparmor=$profile" \
  "$image"

if ! sudo journalctl -k --since "$started" --no-pager \
  | grep "$profile" \
  | grep -q 'apparmor="DENIED".*family="inet"'; then
  echo "Connection was blocked, but no matching AppArmor audit record was found" >&2
  exit 1
fi

echo "PASS: outbound request was blocked and recorded by AppArmor"
