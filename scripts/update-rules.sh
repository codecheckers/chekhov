#!/usr/bin/env bash
# Refresh the bundled copies of the CODECHECK validation rules.
#
# The rules are maintained in the register, one file per version of the
# configuration file specification:
#   https://github.com/codecheckers/register/blob/master/RULES.md
#
# The copies in internal/rules/data/ are embedded in the binary, so the bot
# needs no network to validate a codecheck.yml. provenance.json records which
# register commit they came from, and the tests check it against the files.
#
#   scripts/update-rules.sh                 # from the register on GitHub
#   scripts/update-rules.sh ../register     # from a local checkout
set -euo pipefail

repo="codecheckers/register"
raw="https://raw.githubusercontent.com/${repo}/master/"
target="$(dirname "$0")/../internal/rules/data"
local_checkout="${1:-}"
versions=("1.0" "2.0")

retrieved="$(date -u +%Y-%m-%dT%H:%M:%S%z)"
entries=()

for version in "${versions[@]}"; do
  file="rules-${version}.yml"
  if [ -n "$local_checkout" ]; then
    cp "${local_checkout}/${file}" "${target}/${file}"
    commit="$(git -C "$local_checkout" log -1 --format=%H -- "$file")"
    commit_date="$(git -C "$local_checkout" log -1 --format=%cI -- "$file")"
    via="local register checkout"
  else
    curl -fsSL "${raw}${file}" -o "${target}/${file}"
    commit="$(curl -fsSL "https://api.github.com/repos/${repo}/commits?path=${file}&per_page=1" |
      grep -m1 '"sha"' | cut -d'"' -f4)"
    commit_date="$(curl -fsSL "https://api.github.com/repos/${repo}/commits?path=${file}&per_page=1" |
      grep -m1 '"date"' | cut -d'"' -f4)"
    via="download"
  fi

  count="$(grep -c '^  - id:' "${target}/${file}")"
  md5="$(md5sum "${target}/${file}" | cut -d' ' -f1)"
  entries+=("$(printf '    {\n      "file": "%s",\n      "spec_version": "%s",\n      "rules": %s,\n      "commit": "%s",\n      "commit_date": "%s",\n      "md5": "%s"\n    }' \
    "$file" "$version" "$count" "$commit" "$commit_date" "$md5")")
  echo "${file}: ${count} rules for specification ${version}"
done

{
  printf '{\n'
  printf '  "comment": "Written by scripts/update-rules.sh. The rules themselves are maintained in the register, see https://github.com/codecheckers/register/blob/master/RULES.md",\n'
  printf '  "source": "%s",\n' "$raw"
  printf '  "retrieved": "%s",\n' "$retrieved"
  printf '  "via": "%s",\n' "$via"
  printf '  "files": [\n'
  printf '%s' "$(IFS=$'\n'; printf '%s' "${entries[0]}"; for entry in "${entries[@]:1}"; do printf ',\n%s' "$entry"; done)"
  printf '\n  ]\n}\n'
} > "${target}/provenance.json"

echo "provenance written to ${target}/provenance.json"
