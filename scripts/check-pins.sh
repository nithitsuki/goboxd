#!/usr/bin/env bash
# check-pins.sh - verify that build inputs are version-pinned.
#
# Rules:
#   - Every real `apt-get install` invocation in scripts/lang_install/*.sh
#     and in the Dockerfile must pin each package with pkg=VERSION
#     (no floating package names).
#   - Every `FROM` line in the Dockerfile must pin the base image with
#     @sha256:... (no floating tags).
#
# Shell statements and Dockerfile RUN lines are joined across backslash
# continuations before inspection. Quoted heredoc bodies are not build-time
# commands, so the lint ignores their content. Lines starting with '#' are
# comments and are skipped. Run from anywhere:  scripts/check-pins.sh
set -u

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
violations=""

# Join backslash-continuations. Mask heredoc bodies so their text is not
# inspected as shell statements.
join_statements() {
  awk '
    function emit(s) { if (s != "") print s }
    {
      if (in_heredoc) {
        if ($0 == heredoc) in_heredoc = 0
        next
      }
      if (match($0, /<<[-]?['\''"]?[A-Za-z_][A-Za-z0-9_]*/)) {
        start = RSTART
        while (substr($0, start, 1) !~ /[A-Za-z_]/) start++
        heredoc = ""
        for (i = start; i < RSTART + RLENGTH; i++) {
          c = substr($0, i, 1)
          if (c ~ /[A-Za-z0-9_]/) heredoc = heredoc c
          else break
        }
        in_heredoc = 1
        next
      }
      if (sub(/\\$/, "", $0)) { stmt = stmt " " $0; next }
      stmt = stmt " " $0
      emit(stmt)
      stmt = ""
    }
    END { emit(stmt) }
  ' "$1"
}

# Check one joined statement for a floating `apt-get install` package name.
lint_statement() {
  local file="$1" stmt="$2" rest tok
  # Skip comments (first non-space character is '#').
  [[ "$stmt" =~ ^[[:space:]]*# ]] && return
  if printf '%s\n' "$stmt" | grep -Eq 'apt-get.*install'; then
    # Everything after the last standalone `install` command token is
    # options + packages. The word boundary keeps the -install- substring
    # inside --no-install-recommends from confusing the split.
    rest="$(printf '%s\n' "$stmt" | sed -E 's/.*[[:space:]]install([[:space:]]|$)/ /')"
    for tok in $rest; do
      case "$tok" in
        -* ) continue ;;
        '&&' | '||' | ';' ) break ;;  # shell continuation: no more packages
        '>' | '|' | 'then' | 'fi' | 'if' ) continue ;;
      esac
      # Strip trailing shell punctuation (e.g. "nginx;" from "install nginx;").
      stripped="$tok"
      while [[ "$stripped" == *[';&|'] ]]; do stripped="${stripped%?}"; done
      case "$stripped" in
        *=*) ;;
        *) violations="${violations}${file}: unpinned apt package '${stripped}' (use pkg=VERSION)\n" ;;
      esac
      # A trailing operator ends the package list; do not inspect what follows.
      [[ "$tok" != "$stripped" ]] && break
    done
  fi
}

# Lint the language install scripts.
for f in "$repo"/scripts/lang_install/*.sh; do
  while IFS= read -r stmt; do
    [ -z "$stmt" ] && continue
    lint_statement "$f" "$stmt"
  done < <(join_statements "$f")
done

# Lint apt-get installs inside Dockerfile RUN statements.
while IFS= read -r stmt; do
  [ -z "$stmt" ] && continue
  lint_statement "$repo/Dockerfile" "$stmt"
done < <(join_statements "$repo/Dockerfile")

# Every FROM line must pin its base image digest.
while IFS= read -r line; do
  case "$line" in
    FROM*)
      if ! printf '%s\n' "$line" | grep -q '@sha256'; then
        violations="${violations}Dockerfile: unpinned base image '${line}' (use @sha256:...)\n"
      fi
      ;;
  esac
done < "$repo/Dockerfile"

if [ -n "$violations" ]; then
  printf 'check-pins: version-pin lint FAILED\n%b' "$violations" >&2
  exit 1
fi

echo "check-pins: OK (all apt installs pinned, all FROM lines digest-pinned)"
