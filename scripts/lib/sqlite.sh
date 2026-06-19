#!/usr/bin/env bash

require_sqlite3() {
  if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "sqlite3 is required for SQLite backup/restore operations." >&2
    exit 1
  fi
}

require_sha256_tool() {
  if command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1; then
    return
  fi
  echo "sha256sum or shasum is required for backup checksum operations." >&2
  exit 1
}

sqlite_write_sha256() {
  local file="$1"
  local dir base
  dir="$(dirname "$file")"
  base="$(basename "$file")"
  require_sha256_tool
  (
    cd "$dir"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum "$base" > "$base.sha256"
    else
      shasum -a 256 "$base" > "$base.sha256"
    fi
  )
}

sqlite_verify_sha256() {
  local sha_file="$1"
  local dir base
  dir="$(dirname "$sha_file")"
  base="$(basename "$sha_file")"
  require_sha256_tool
  (
    cd "$dir"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum -c "$base"
    else
      shasum -a 256 -c "$base"
    fi
  )
}

sqlite_path_from_dsn() {
  local dsn="$1"
  local path="$dsn"
  if [[ "$path" == file:* ]]; then
    path="${path#file:}"
    path="${path%%\?*}"
    path="${path%%#*}"
  fi
  if [[ -z "$path" || "$path" == ":memory:" || "$dsn" == *"mode=memory"* ]]; then
    echo "DATABASE_DSN must point to a file-backed SQLite database for backup." >&2
    exit 1
  fi
  printf '%s\n' "$path"
}

sqlite_abs_path() {
  local path="$1"
  local base_dir="$2"
  if [[ "$path" == /* ]]; then
    printf '%s\n' "$path"
  else
    printf '%s/%s\n' "$base_dir" "$path"
  fi
}

sqlite_verify_integrity() {
  local db_path="$1"
  local integrity
  integrity="$(sqlite3 "$db_path" 'PRAGMA integrity_check;')"
  if [[ "$integrity" != "ok" ]]; then
    echo "SQLite integrity_check failed for $db_path: $integrity" >&2
    exit 1
  fi

  local foreign_key_errors
  foreign_key_errors="$(sqlite3 "$db_path" 'PRAGMA foreign_key_check;')"
  if [[ -n "$foreign_key_errors" ]]; then
    echo "SQLite foreign_key_check failed for $db_path:" >&2
    echo "$foreign_key_errors" >&2
    exit 1
  fi
}

sqlite_require_tables() {
  local db_path="$1"
  shift
  local table exists
  for table in "$@"; do
    exists="$(sqlite3 "$db_path" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='$table';")"
    if [[ "$exists" != "1" ]]; then
      echo "Required table '$table' not found in $db_path" >&2
      exit 1
    fi
  done
}

sqlite_print_table_counts() {
  local db_path="$1"
  shift
  local table count
  for table in "$@"; do
    count="$(sqlite3 "$db_path" "SELECT count(*) FROM $table;")"
    printf '  %-28s %s\n' "$table" "$count"
  done
}

sqlite_print_schema_migrations() {
  local db_path="$1"
  local has_table
  has_table="$(sqlite3 "$db_path" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations';")"
  if [[ "$has_table" == "1" ]]; then
    sqlite3 "$db_path" "SELECT version || ' ' || name || ' ' || checksum FROM schema_migrations ORDER BY version;"
  fi
}
