#!/usr/bin/env bash
# One-time offline compaction for Eyrie DuckDB databases.
#
# DuckDB CHECKPOINT reclaims free blocks for reuse but does not shrink the OS
# file. This script copies the database into a fresh file via
# `COPY FROM DATABASE`, which yields a compacted file with no free blocks.
#
# The compacted file is written next to the source (or to OUTPUT). The script
# never modifies or swaps the source; an operator must stop the server, back up
# the source, swap in the compacted file, and restart. No row data is logged.
#
# Usage:
#   scripts/compact_duckdb.sh /path/to/database.db [output.db]
#
# Requirements: duckdb CLI on PATH (https://duckdb.org/docs/installation)

set -euo pipefail

usage() {
    cat <<'EOF'
Usage: scripts/compact_duckdb.sh <source.db> [output.db]

  source.db   path to the DuckDB file to compact (opened read-only)
  output.db   optional target path; defaults to <source.db>.compacted

Creates a compacted copy and prints both file sizes. Does not replace the
source. To deploy: stop the server, back up the source, swap in the compacted
file, restart.
EOF
    exit 1
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
    usage
fi

SOURCE="$1"
OUTPUT="${2:-${SOURCE}.compacted}"

if ! command -v duckdb >/dev/null 2>&1; then
    echo "error: duckdb CLI not found on PATH" >&2
    exit 1
fi

if [[ ! -f "$SOURCE" ]]; then
    echo "error: source database not found: $SOURCE" >&2
    exit 1
fi

if [[ "$SOURCE" == "$OUTPUT" ]]; then
    echo "error: output path must differ from source path" >&2
    exit 1
fi

if [[ -e "$OUTPUT" ]]; then
    echo "error: output path already exists: $OUTPUT" >&2
    exit 1
fi

echo "compacting: $SOURCE -> $OUTPUT"
duckdb -c "
PRAGMA memory_limit='4GB';
ATTACH '$SOURCE' AS source_db (READ_ONLY);
ATTACH '$OUTPUT' AS target_db;
COPY FROM DATABASE source_db TO target_db;
"

source_size=$(stat -c %s "$SOURCE")
target_size=$(stat -c %s "$OUTPUT")

echo "done: source ${source_size} bytes -> target ${target_size} bytes"
echo ""
echo "next steps (manual):"
echo "  1. stop the Eyrie server"
echo "  2. back up $SOURCE"
echo "  3. replace it with $OUTPUT"
echo "  4. start the server and verify the status page"
