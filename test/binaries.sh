#!/usr/bin/env bash
# bin/check-binaries refuses an ELF and a big file, lets text through, honours the allow-list, and the
# real repository is clean. Offline.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0
ok() { echo "ok    $1"; }
ko() { echo "FAIL  $1"; fail=1; }

mkdir -p "$work/r/bin"
cp "$root/bin/check-binaries" "$work/r/bin/"
cd "$work/r"
git init -q .
git config user.email t@example.com
git config user.name t

echo "text" > a.txt
git add a.txt bin/check-binaries
bin/check-binaries staged && ok "a text file passes" || ko "a text file passes"

printf '\177ELF\002\001\001\000' > prog
git add prog
if bin/check-binaries staged 2>/dev/null; then ko "an ELF is refused"; else ok "an ELF is refused"; fi
git reset -q prog && rm prog

printf 'MZ\220\000\003\000' > prog.exe
git add prog.exe
if bin/check-binaries staged 2>/dev/null; then ko "a PE program is refused"; else ok "a PE program is refused"; fi
git reset -q prog.exe && rm prog.exe

head -c 2000000 /dev/zero > big.dat
git add big.dat
if bin/check-binaries staged 2>/dev/null; then ko "a file over 1 MiB is refused"; else ok "a file over 1 MiB is refused"; fi

printf 'big.dat\n' > .binaries-allowed
bin/check-binaries staged && ok "an allowed path passes" || ko "an allowed path passes"
git reset -q big.dat && rm big.dat .binaries-allowed

printf '\177ELF\002\001\001\000' > prog
git add prog
git commit -q -m "binary" --no-verify
if bin/check-binaries tracked 2>/dev/null; then ko "a tracked ELF is refused"; else ok "a tracked ELF is refused"; fi

cd "$root"
bin/check-binaries tracked && ok "this repository holds no binary" || ko "this repository holds no binary"
[ "$fail" = 0 ] && echo "binaries test: all checks passed"
exit "$fail"
