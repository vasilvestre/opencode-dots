#!/usr/bin/env bash
# Offline test of deploy/types/local/team-access.sh: a fake ssh runs the server side against a
# temporary home. Also run by deploy/test/offline.sh.
#
#   deploy/test/team-access.sh
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
srv=$work/srv
mkdir -p "$srv" "$work/bin" "$work/keys"
cat >"$work/bin/ssh" <<'FAKE'
#!/usr/bin/env bash
while [ $# -gt 0 ] && [ "$1" = -o ]; do shift 2; done
shift
cd "$FAKE_SRV" && HOME=$FAKE_SRV bash -c "$*"
FAKE
chmod +x "$work/bin/ssh"
export FAKE_SRV=$srv PATH="$work/bin:$PATH"
t=deploy/types/local/team-access.sh
user=dots@fake
ak=$srv/.ssh/authorized_keys

fail=0
check() { local d=$1; shift; if "$@" >/dev/null 2>&1; then echo "ok    $d"; else echo "FAIL  $d"; fail=1; fi; }
refuses() { local d=$1; shift; if "$@" >/dev/null 2>&1; then echo "FAIL  $d (it succeeded)"; fail=1; else echo "ok    $d"; fi; }
nbak() { find "$srv/.ssh" -name 'authorized_keys.bak-*' | wc -l; }
body() { awk '{print $2}' "$1"; }

for n in alice bob alice2 ec; do
  kt=ed25519
  [ "$n" = ec ] && kt=ecdsa
  ssh-keygen -q -t $kt -N '' -C "$n@laptop" -f "$work/keys/$n"
done
cat "$work/keys/alice.pub" "$work/keys/bob.pub" >"$work/keys/two.pub"
printf 'restrict %s' "$(cat "$work/keys/bob.pub")" >"$work/keys/opt.pub"

# a server with a key of the admin already there, modes wider than needed
mkdir -p "$srv/.ssh"
chmod 755 "$srv/.ssh"
printf '# admin\nssh-ed25519 AAAAadminadmin admin@box\n' >"$ak"
chmod 644 "$ak"
cp "$ak" "$work/ak.orig"

out=$($t $user add alice "$work/keys/alice.pub" --dry-run)
want="restrict,port-forwarding,permitopen=\"127.0.0.1:8098\",command=\"/bin/false\" $(awk '{print $1, $2}' "$work/keys/alice.pub") dots-team:alice"
check "dry-run prints the line" test "$out" = "$want"
check "dry-run touches nothing" cmp "$ak" "$work/ak.orig"

check "add alice" $t $user add alice "$work/keys/alice.pub"
check "the line is exactly the format" grep -qxF "$want" "$ak"
check "the admin line is untouched" grep -qxF 'ssh-ed25519 AAAAadminadmin admin@box' "$ak"
check "the comment line is untouched" grep -qxF '# admin' "$ak"
check "authorized_keys is 600" test "$(stat -c %a "$ak")" = 600
check ".ssh is 700" test "$(stat -c %a "$srv/.ssh")" = 700
check "a backup was kept" test "$(nbak)" = 1
check "the backup is the old file" cmp "$(find "$srv/.ssh" -name 'authorized_keys.bak-*')" "$work/ak.orig"
check "no temporary file is left" test "$(find "$srv/.ssh" -name 'authorized_keys.??????' | wc -l)" = 0

cp "$ak" "$work/after1"
check "adding the same key again" $t $user add alice "$work/keys/alice.pub"
check "it does not duplicate" test "$(grep -c 'dots-team:alice$' "$ak")" = 1
check "and changes nothing" cmp "$ak" "$work/after1"

check "replace alice with another key and port" $t $user add alice "$work/keys/alice2.pub" --port 9000
check "still one line for alice" test "$(grep -c 'dots-team:alice$' "$ak")" = 1
check "it is the new key and port" grep -q "permitopen=\"127.0.0.1:9000\".*$(body "$work/keys/alice2.pub")" "$ak"
check "the old key is gone" test -z "$(grep -F "$(body "$work/keys/alice.pub")" "$ak" || true)"

check "add bob" $t $user add bob "$work/keys/bob.pub"
check "add an ecdsa key" $t $user add carol-2 "$work/keys/ec.pub"
lst=$($t $user list)
check "list names three developers" test "$(printf '%s\n' "$lst" | wc -l)" = 3
check "list shows a fingerprint and port" grep -q '^alice port=9000 SHA256:' <<<"$lst"
check "list shows no key body" test -z "$(grep -F "$(body "$work/keys/bob.pub")" <<<"$lst" || true)"
check "list never shows the admin" test -z "$(grep admin <<<"$lst" || true)"

refuses "a private key" $t $user add dave "$work/keys/bob"
refuses "a file with several keys" $t $user add dave "$work/keys/two.pub"
refuses "a key that already carries options" $t $user add dave "$work/keys/opt.pub"
refuses "a missing file" $t $user add dave "$work/keys/nope.pub"
refuses "a bad name (upper case)" $t $user add Dave "$work/keys/bob.pub"
refuses "a bad name (leading dash)" $t $user add -dave "$work/keys/bob.pub"
refuses "a bad name (too long)" $t $user add "a$(printf 'b%.0s' $(seq 31))" "$work/keys/bob.pub"
refuses "a bad port" $t $user add dave "$work/keys/bob.pub" --port 99999
check "no dave" test -z "$(grep dave "$ak" || true)"

check "remove bob" $t $user remove bob
check "bob is gone" test -z "$(grep 'dots-team:bob$' "$ak" || true)"
check "alice and carol-2 stay" test "$(grep -c 'dots-team:' "$ak")" = 2
check "the admin line stays" grep -qxF 'ssh-ed25519 AAAAadminadmin admin@box' "$ak"
check "authorized_keys stays 600" test "$(stat -c %a "$ak")" = 600
check "removing an unknown name is harmless" $t $user remove nobody
check "remove al does not match alice" $t $user remove al
check "alice is still there" grep -q 'dots-team:alice$' "$ak"

if [ "$fail" = 0 ]; then echo "team-access test: all checks passed"; else echo "team-access test: failures above" >&2; exit 1; fi
