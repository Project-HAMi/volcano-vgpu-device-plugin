#!/bin/sh
# Install hook files without truncating libraries mapped by running containers.
set -u

src=${VGPU_HOOK_SRC:-/k8s-vgpu/lib/nvidia}
dst=${HOOK_PATH:-/usr/local/vgpu}
tmp=

fail() { echo "vgpu hook install: ERROR: $*" >&2; exit 1; }
cleanup() { [ -z "$tmp" ] || rm -f -- "$tmp"; }
trap cleanup 0
trap 'exit 1' HUP INT TERM

[ -d "$src" ] || fail "source directory missing: $src"
for name in libvgpu.so ld.so.preload; do
  [ -f "$src/$name" ] && [ -r "$src/$name" ] || fail "required file missing or unreadable: $src/$name"
done
mkdir -p -- "$dst" || fail "cannot create destination: $dst"

# Return 0 for equal, 1 for different, and 2 for comparison errors.
files_equal() {
  if command -v cmp >/dev/null 2>&1; then
    cmp -s -- "$1" "$2"
    return $?
  fi
  if command -v md5sum >/dev/null 2>&1; then
    src_md5=$(md5sum < "$1") || return 2
    dst_md5=$(md5sum < "$2") || return 2
    [ "$src_md5" = "$dst_md5" ] && return 0
    return 1
  fi
  echo "vgpu hook install: no cmp or md5sum available" >&2
  return 2
}

for s in "$src"/*; do
  [ -f "$s" ] || continue
  d=$dst/${s##*/}
  [ ! -L "$d" ] || fail "destination is a symlink: $d"
  if [ -e "$d" ]; then
    [ -f "$d" ] || fail "destination is not a regular file: $d"
    if files_equal "$s" "$d"; then
      echo "vgpu hook install: unchanged: $d"
      continue
    else
      status=$?
      [ "$status" -eq 1 ] || fail "cannot compare: $s and $d"
    fi
  fi

  # Publish a unique temporary file on the same filesystem with one rename.
  tmp=$(mktemp "$dst/.${s##*/}.tmp.XXXXXXXXXX") || fail "cannot create temporary file in $dst"
  cp -p -- "$s" "$tmp" || fail "cannot copy: $s"
  mv -fT -- "$tmp" "$d" || fail "cannot replace: $d"
  tmp=
  echo "vgpu hook install: updated: $d"
done
