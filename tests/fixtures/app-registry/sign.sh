#!/usr/bin/env bash
# Generate keys (first run) and (re)sign every manifest + registry.json
# in this fixture. Idempotent: re-running after manifest edits replaces
# the .sig files. Private key stays under keys/ (gitignored).
#
# Usage:
#   bash tests/fixtures/app-registry/sign.sh
#
# Then either:
#   bash tests/fixtures/app-registry/serve.sh   # serve over :9999
# or
#   make app-registry-serve
#
# On the VM, register once:
#   kura app registry add --name dev \
#     --url http://<this-host>:9999 \
#     --identity 'kuraos-dev-fixture'
# Then install via /ui/admin/apps store tab.
set -euo pipefail

cd "$(dirname "$0")"
ROOT="$PWD"
KEYS="$ROOT/keys"
KEY_ID="kuraos-dev-fixture"
IDENTITY="kuraos-dev-fixture"
PRIV="$KEYS/dev-signer.key"
PUB="$KEYS/dev-signer.pub"

# Need kura binary somewhere — use the built one in repo bin/, fall back to PATH.
if [[ -x "../../../bin/kura" ]]; then
  KURA="../../../bin/kura"
elif command -v kura >/dev/null 2>&1; then
  KURA="kura"
else
  echo "error: kura binary not found. Run 'make build' first." >&2
  exit 1
fi

mkdir -p "$KEYS"

# Step 1: keygen if not present. The CLI writes the .pub into the directory
# named by --dir, with filename <key_id>.pub. We want a stable filename here
# so we move it. The private key comes back via stdout (base64).
if [[ ! -f "$PRIV" ]]; then
  echo "==> generating dev signing key (keys/dev-signer.{key,pub})"
  out="$("$KURA" app keygen --dir "$KEYS")"
  generated_id="$(echo "$out" | sed -n 's/^key_id=//p')"
  priv_b64="$(echo "$out" | sed -n 's/^priv_b64=//p')"
  if [[ -z "$generated_id" || -z "$priv_b64" ]]; then
    echo "error: kura app keygen output unexpected:" >&2
    echo "$out" >&2
    exit 2
  fi
  echo "$priv_b64" > "$PRIV"
  chmod 600 "$PRIV"
  # Rename <generated_id>.pub → dev-signer.pub for fixed reference + put one
  # under the canonical key id name too (which is what LocalVerifier scans for).
  cp "$KEYS/${generated_id}.pub" "$PUB"
  if [[ "$generated_id" != "$KEY_ID" ]]; then
    # The verifier matches by file basename → key id mapping. Make a copy
    # under the stable KEY_ID so operators can reason about it without
    # caring about the random one kura keygen produced.
    cp "$KEYS/${generated_id}.pub" "$KEYS/${KEY_ID}.pub"
  fi
  echo "    key_id (random) = $generated_id"
  echo "    key_id (stable) = $KEY_ID  (used by sign)"
fi

PRIV_B64="$(cat "$PRIV")"

# Step 2: sign every manifest.yaml under apps/.
echo "==> signing manifests"
shopt -s nullglob
for m in apps/*/*/manifest.yaml; do
  echo "    $m"
  "$KURA" app sign \
    --priv "$PRIV_B64" \
    --identity "$IDENTITY" \
    --key-id "$KEY_ID" \
    --file "$m" \
    --out "$m.sig" >/dev/null
done

# Step 3: build registry.json from the manifests we just signed.
echo "==> building registry.json"
python3 - "$ROOT" <<'PY' > registry.json
import hashlib, json, os, sys
from pathlib import Path
from datetime import datetime, timezone

root = Path(sys.argv[1])
apps = {}
for manifest in sorted(root.glob("apps/*/*/manifest.yaml")):
    parts = manifest.relative_to(root).parts  # apps, <name>, <ver>, manifest.yaml
    name, version = parts[1], parts[2]
    sha = hashlib.sha256(manifest.read_bytes()).hexdigest()
    app = apps.setdefault(name, {"versions": {}, "latest": ""})
    app["versions"][version] = {
        "manifest_sha256": sha,
        "released_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
    # latest = lexicographically max version (semver-ish for simple cases)
    if version > app["latest"]:
        app["latest"] = version

doc = {
    "schema_version": "v1",
    "updated_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "apps": apps,
}
print(json.dumps(doc, indent=2, sort_keys=True))
PY

# Step 4: sign registry.json itself.
echo "==> signing registry.json"
"$KURA" app sign \
  --priv "$PRIV_B64" \
  --identity "$IDENTITY" \
  --key-id "$KEY_ID" \
  --file "registry.json" \
  --out "registry.json.sig" >/dev/null

echo "==> done. Apps in registry:"
python3 -c "import json; d=json.load(open('registry.json')); [print(f'  - {n} (latest {v[\"latest\"]})') for n, v in d['apps'].items()]"
echo ""
echo "Next:"
echo "  make app-registry-serve     # then on VM: kura app registry add --name dev --url http://<host>:9999 --identity '$IDENTITY'"
