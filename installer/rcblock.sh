#!/bin/sh
# Sourceable library for idempotently managing a delimited, id-namespaced
# block of content inside a config file (e.g. a shell rc file) without
# disturbing anything else in that file.
#
# Usage:
#   . installer/rcblock.sh
#   rcblock_install "$HOME/.profile" myid <<'EOF'
#   export PATH="/some/dir:$PATH"
#   EOF
#   rcblock_remove "$HOME/.profile" myid
#
# Re-running rcblock_install replaces the previous block for that id in
# place (moved to the end of the file); rcblock_remove is a no-op if the
# file or block doesn't exist. Neither ever touches content outside the
# markers.

rcblock_start_marker() {
  printf '# >>> scripts:%s >>>\n' "$1"
}

rcblock_end_marker() {
  printf '# <<< scripts:%s <<<\n' "$1"
}

# Prints file with the id's block (if any) stripped out. Fails if the
# start/end markers are unbalanced, so callers never guess at a corrupt file.
_rcblock_strip() {
  file="$1"
  id="$2"
  start="$(rcblock_start_marker "$id")"
  end="$(rcblock_end_marker "$id")"
  awk -v start="$start" -v end="$end" '
    BEGIN { skip = 0 }
    $0 == start {
      if (skip) { print "duplicate start marker" > "/dev/stderr"; exit 3 }
      skip = 1
      next
    }
    $0 == end {
      if (!skip) { print "end marker without start" > "/dev/stderr"; exit 4 }
      skip = 0
      next
    }
    skip { next }
    { print }
    END { if (skip) { print "start marker without end" > "/dev/stderr"; exit 2 } }
  ' "$file"
}

# rcblock_install <file> <id>
# Block content is read from stdin. Idempotent: replaces any existing block
# for this id, or appends a new one.
rcblock_install() {
  file="$1"
  id="$2"
  content="$(cat)"

  mkdir -p "$(dirname "$file")"
  [ -f "$file" ] || : > "$file"

  tmp="$(mktemp "${file}.rcblock.XXXXXX")" || return 1
  err=$( { _rcblock_strip "$file" "$id" >"$tmp"; } 2>&1 )
  status=$?
  if [ "$status" -ne 0 ]; then
    rm -f "$tmp"
    echo "rcblock: ${file}: corrupt '${id}' block (${err}); refusing to edit" >&2
    return 1
  fi

  if [ -s "$tmp" ]; then
    case "$(tail -c1 "$tmp")" in
      "") ;;
      *) echo >>"$tmp" ;;
    esac
  fi
  {
    rcblock_start_marker "$id"
    echo "# managed by scripts installer; edits here will be overwritten - see install script --uninstall"
    printf '%s\n' "$content"
    rcblock_end_marker "$id"
  } >>"$tmp"

  cat "$tmp" >"$file"
  rm -f "$tmp"
}

# rcblock_remove <file> <id>
# No-op if the file or block doesn't exist.
rcblock_remove() {
  file="$1"
  id="$2"
  [ -f "$file" ] || return 0

  start="$(rcblock_start_marker "$id")"
  grep -qxF "$start" "$file" 2>/dev/null || return 0

  tmp="$(mktemp "${file}.rcblock.XXXXXX")" || return 1
  err=$( { _rcblock_strip "$file" "$id" >"$tmp"; } 2>&1 )
  status=$?
  if [ "$status" -ne 0 ]; then
    rm -f "$tmp"
    echo "rcblock: ${file}: corrupt '${id}' block (${err}); refusing to edit" >&2
    return 1
  fi

  cat "$tmp" >"$file"
  rm -f "$tmp"
}
