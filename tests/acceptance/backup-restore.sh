#!/usr/bin/env bash
# Acceptance tests for Sprint Ssys001 Story 5 (kura backup / restore).
#
#   [AC-Ssys001-5-1]  TestBackupContainsConfigAndEncryptedVault — tarball
#                     contains config.json (plaintext) and
#                     secrets.kura.age (always age-encrypted).
#   [AC-Ssys001-5-2]  TestRoundTripPassphraseEncrypted — encrypted backup
#                     can be restored with the same passphrase.
#   [AC-Ssys001-5-3]  TestRecipientModeNotYetSupported — `kura backup
#                     --recipient` rejects with a deferred-feature note.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

PASS=0
FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL + 1)); }
pass() { echo "PASS: $*"; PASS=$((PASS + 1)); }

if [ ! -x bin/kura ]; then
    make build >/tmp/kuraos-acceptance-backup-build.log 2>&1 || {
        cat /tmp/kuraos-acceptance-backup-build.log >&2
        echo "FAIL: build prerequisite failed" >&2
        exit 1
    }
fi

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT
DB="$WORKDIR/state.db"
TARBALL="$WORKDIR/backup.tar.gz"
PASSPHRASE="acceptance-test-passphrase-1234"

# Bootstrap an empty state DB.
KURA_STATE_DB="$DB" ./bin/kura version >/dev/null 2>&1 || true
KURA_STATE_DB="$DB" ./bin/kura config export >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# [AC-Ssys001-5-1] kura backup writes a tarball with the case-X layout:
# manifest.json + config.json (plaintext) + secrets.kura.age (encrypted).
# ---------------------------------------------------------------------------
echo "$PASSPHRASE" | KURA_STATE_DB="$DB" ./bin/kura backup -o "$TARBALL" --passphrase-stdin >"$WORKDIR/backup.out" 2>"$WORKDIR/backup.err"
if [ ! -s "$TARBALL" ]; then
    cat "$WORKDIR/backup.err" >&2
    fail "[AC-Ssys001-5-1] backup tarball not created"
else
    pass "[AC-Ssys001-5-1] kura backup wrote $TARBALL"
fi

# Inspect tarball contents — must contain exactly the three documented files.
GOT_FILES=$(tar tzf "$TARBALL" | sort | tr '\n' ' ')
WANT_FILES="config.json manifest.json secrets.kura.age "
if [ "$GOT_FILES" != "$WANT_FILES" ]; then
    fail "[AC-Ssys001-5-1] tarball layout mismatch (got: $GOT_FILES, want: $WANT_FILES)"
else
    pass "[AC-Ssys001-5-1] tarball contains exactly: manifest.json + config.json + secrets.kura.age"
fi

# secrets.kura.age must NOT contain the literal passphrase or any
# obvious credential bytes (we have an empty vault here, so this is a
# header check — age envelope starts with 'age-encryption.org/v1').
tar xzf "$TARBALL" -C "$WORKDIR" secrets.kura.age
if head -c 64 "$WORKDIR/secrets.kura.age" | grep -q "age-encryption.org/v1"; then
    pass "[AC-Ssys001-5-1] secrets.kura.age has age envelope signature"
else
    fail "[AC-Ssys001-5-1] secrets.kura.age missing age envelope (vault might be plaintext)"
fi

# ---------------------------------------------------------------------------
# [AC-Ssys001-5-2] Round-trip: restore the tarball into a fresh state DB.
# ---------------------------------------------------------------------------
DB2="$WORKDIR/state2.db"
KURA_STATE_DB="$DB2" ./bin/kura version >/dev/null 2>&1 || true
KURA_STATE_DB="$DB2" ./bin/kura config export >/dev/null 2>&1 || true

if echo "$PASSPHRASE" | KURA_STATE_DB="$DB2" ./bin/kura restore --passphrase-stdin --config-out "$WORKDIR/restored.json" "$TARBALL" >"$WORKDIR/restore.out" 2>"$WORKDIR/restore.err"; then
    if grep -q "vault restored" "$WORKDIR/restore.out"; then
        pass "[AC-Ssys001-5-2] restore reports successful vault import"
    else
        cat "$WORKDIR/restore.out" >&2
        fail "[AC-Ssys001-5-2] restore output missing 'vault restored'"
    fi
else
    cat "$WORKDIR/restore.err" >&2
    fail "[AC-Ssys001-5-2] kura restore failed"
fi

if [ ! -s "$WORKDIR/restored.json" ]; then
    fail "[AC-Ssys001-5-2] restored config.json is empty"
else
    pass "[AC-Ssys001-5-2] restored config.json materialised"
fi

# Wrong passphrase MUST fail.
if echo "totally-wrong-passphrase" | KURA_STATE_DB="$DB2" ./bin/kura restore --passphrase-stdin "$TARBALL" >/dev/null 2>"$WORKDIR/wrongpw.err"; then
    fail "[AC-Ssys001-5-2] restore with wrong passphrase succeeded — should fail"
else
    pass "[AC-Ssys001-5-2] restore with wrong passphrase fails (vault stays sealed)"
fi

# ---------------------------------------------------------------------------
# [AC-Ssys001-5-3] Recipient mode is deferred to v1.x — kura backup
# --recipient must reject with a clear "deferred" message.
# ---------------------------------------------------------------------------
if echo "$PASSPHRASE" | KURA_STATE_DB="$DB" ./bin/kura backup -o "$WORKDIR/recip.tar" --recipient --passphrase-stdin >"$WORKDIR/recip.out" 2>"$WORKDIR/recip.err"; then
    fail "[AC-Ssys001-5-3] --recipient unexpectedly succeeded; should be deferred"
else
    if grep -q "v1.x" "$WORKDIR/recip.err" || grep -q "deferred" "$WORKDIR/recip.err"; then
        pass "[AC-Ssys001-5-3] --recipient rejected with deferred-feature note"
    else
        cat "$WORKDIR/recip.err" >&2
        fail "[AC-Ssys001-5-3] --recipient rejection message did not mention deferred / v1.x"
    fi
fi

echo
echo "backup-restore.sh: $PASS passed, $FAIL failed"
exit "$FAIL"
