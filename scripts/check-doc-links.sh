#!/usr/bin/env bash
#
# Verify that markdown links and file:line citations in the living docs
# point at real files.
#
# Why: doc drift — used-but-undefined reference links, stale file:line
# citations — is only caught by a manual full-document read otherwise. This
# guard makes that drift class fail the gate instead of waiting for the
# next audit.
#
# Scope: root-level *.md only. docs/ subdirectories hold historical records
# whose citations intentionally describe past states. Fenced code blocks
# are excluded: their contents are examples, not links.
#
# Checks:
#   1. Relative markdown link targets exist ([text](target) and images).
#      http(s)/mailto: targets and pure #anchors are skipped; #fragments
#      are stripped before resolution.
#   2. Reference-style links are defined: every [name] used as a shortcut
#      or [text][name] full reference needs a [name]: definition. Inline
#      code spans and task-list checkboxes are excluded.
#   3. Backtick file citations (`errors.go:28`, `dir/script.sh`) resolve to
#      real files, and a given line number is within the file. Citations
#      containing a glob character (`*`) are matched with find.
#
# Usage:
#   scripts/check-doc-links.sh
#
# Exit codes: 0 = all links resolve, 1 = findings, 2 = usage/environment.

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

failures=0
say_fail() {
	echo "check-doc-links: $*" >&2
	failures=$((failures + 1))
}

# is_citable <text>: looks like a bare relative path, not a command,
# placeholder, home shortcut, or Windows path.
is_citable() {
	local cite=$1
	[[ $cite != *" "* && $cite != *'"'* && $cite != *"'"* &&
		$cite != *"<"* && $cite != *">"* && $cite != *"%"* &&
		$cite != *"\$"* && $cite != *"\\"* && $cite != "~"* &&
		$cite != *"..v"* && ! ($cite =~ ^\.[a-z0-9]+$) ]]
}

# resolve <doc> <target>: a relative link target that exists.
resolve() {
	local doc=$1 target=$2
	target=${target%%#*}

	case "$target" in
	"" | "." | "..") return 0 ;;
	http://* | https://* | mailto:* | cid:*) return 0 ;;
	/*) target=${target#/} ;;
	*) target=$(dirname "$doc")/$target ;;
	esac

	[[ -e $target ]]
}

# find_glob <pattern>: any path in the tree matches the glob.
find_glob() {
	find . -path "./$1" -print -quit 2>/dev/null | grep -q .
}

for doc in ./*.md; do
	# Strip fenced code blocks once; all checks run on prose only.
	prose=$(awk '/^```/ { infence = !infence; next } !infence' "$doc")

	# --- check 1: relative link targets (links and images share ](target))
	while IFS= read -r target; do
		if ! resolve "$doc" "$target"; then
			say_fail "$doc: link target does not exist: $target"
		fi
	done < <(grep -hoE '\]\([^)]+\)' <<<"$prose" | sed -e 's/^\](//' -e 's/)$//' || true)

	# --- check 2: undefined reference-style links
	while IFS= read -r name; do
		say_fail "$doc: reference link [$name] is used but never defined"
	done < <(awk '
		{
			line = $0
			gsub(/`[^`]*`/, "", line)                     # inline code spans
			if (match(line, /^(\s*[-*]\s*)?\[[^]]+\]:/)) {  # definitions
				def = line
				sub(/^\s*[-*]\s*/, "", def)
				sub(/^\[/, "", def)
				sub(/\]:.*/, "", def)
				defs[def] = 1
				next
			}
			gsub(/!\[[^]]*\]\([^)]*\)/, "", line)         # images
			gsub(/\[[^]]*\]\([^)]*\)/, "", line)          # inline links
			while (match(line, /\[[^]]*\]\[[^]]*\]/)) {   # full refs [t][n]
				r = substr(line, RSTART, RLENGTH)
				sub(/^.*\]\[/, "", r)
				sub(/\]$/, "", r)
				if (r !~ /^[ xX]$/) uses[r] = 1
				line = substr(line, 1, RSTART - 1) substr(line, RSTART + RLENGTH)
			}
			while (match(line, /\[[^]]+\]/)) {            # shortcut refs [n]
				r = substr(line, RSTART + 1, RLENGTH - 2)
				if (r !~ /^[ xX]$/) uses[r] = 1
				line = substr(line, 1, RSTART - 1) substr(line, RSTART + RLENGTH)
			}
		}
		END {
			for (u in uses) if (!(u in defs)) print u
		}
	' <<<"$prose")

	# --- check 3: backtick file citations with optional :line
	while IFS= read -r cite; do
		if ! is_citable "$cite"; then
			continue
		fi

		file=${cite%%:*}
		line=${cite##*:}

		if [[ $file == *"*"* ]]; then
			if ! find_glob "$file"; then
				say_fail "$doc: citation glob matches nothing: $file"
			fi
			continue
		fi

		if [[ ! -e $file ]]; then
			say_fail "$doc: cited file does not exist: $cite"
			continue
		fi

		if [[ $file != "$cite" && $file =~ \.(go|yml|yaml|sh|md|txt|json|nix|mod)$ ]]; then
			if ! [[ $line =~ ^[0-9]+$ ]] || ((line < 1)) || ((line > $(wc -l <"$file"))); then
				say_fail "$doc: cited line out of range: $cite (file has $(wc -l <"$file") lines)"
			fi
		fi
	done < <(grep -hoE '`[^`]+`' <<<"$prose" | tr -d '`' | grep -E '\.(go|yml|yaml|sh|md|txt|json|nix|mod)(:[0-9]+)?$' || true)
done

if ((failures > 0)); then
	echo "check-doc-links: $failures finding(s) — fix the docs or the code they cite" >&2
	exit 1
fi

echo "check-doc-links: OK"
