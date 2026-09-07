#!/usr/bin/env bash
#
# Shared helpers for the scripts in hack/.
#
# Not executable on its own; source it.

# load_dotenv reads KEY=VALUE lines from a file into the environment.
#
# Task already loads .env for anything run through it, but these scripts are
# also meant to be run directly. Doing it here means `./hack/install-holmes.sh`
# and `task holmes:install` behave identically.
#
# A variable already set in the environment is left alone, so an explicit
# `OPENROUTER_API_KEY=... ./hack/demo.sh` still wins over the file — the same
# precedence Task applies.
load_dotenv() {
  local file="${1:-.env}"
  [[ -f "$file" ]] || return 0

  local line key value
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"                       # tolerate CRLF
    line="${line#"${line%%[![:space:]]*}"}"    # strip leading whitespace

    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" == export\ * ]] && line="${line#export }"
    [[ "$line" != *=* ]] && continue

    key="${line%%=*}"
    value="${line#*=}"
    key="${key//[[:space:]]/}"

    # Only plausible shell identifiers; anything else is a malformed line.
    [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue

    # Strip one layer of matching quotes.
    if [[ "$value" == \"*\" || "$value" == \'*\' ]]; then
      value="${value:1:${#value}-2}"
    fi

    # Already set in the environment? Leave it.
    [[ -n "${!key-}" ]] && continue

    export "$key=$value"
  done < "$file"
}
