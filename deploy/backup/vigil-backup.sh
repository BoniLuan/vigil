#!/bin/sh
set -eu

backup_dir=${VIGIL_BACKUP_DIR:-/home/luan/.config/vigil/backups}
postgres_container=${VIGIL_POSTGRES_CONTAINER:-vigil-postgres-1}
retention_days=${VIGIL_BACKUP_RETENTION_DAYS:-30}

case "$retention_days" in
  ''|*[!0-9]*) echo "VIGIL_BACKUP_RETENTION_DAYS must be a positive integer" >&2; exit 2 ;;
esac
if [ "$retention_days" -lt 1 ]; then
  echo "VIGIL_BACKUP_RETENTION_DAYS must be at least 1" >&2
  exit 2
fi

umask 077
mkdir -p "$backup_dir"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
temporary=$(mktemp "$backup_dir/.vigil-$timestamp.XXXXXX.dump")
final="$backup_dir/vigil-$timestamp.dump"
cleanup() { rm -f "$temporary"; }
trap cleanup EXIT HUP INT TERM

docker exec "$postgres_container" pg_dump --format=custom --no-owner --no-acl -U vigil -d vigil > "$temporary"
test -s "$temporary"
docker exec -i "$postgres_container" pg_restore --list < "$temporary" > /dev/null
mv "$temporary" "$final"
trap - EXIT HUP INT TERM

# Only this script's timestamped archives are eligible for local rotation.
find "$backup_dir" -maxdepth 1 -type f -name 'vigil-????????T??????Z.dump' -mtime "+$retention_days" -delete
printf 'validated backup created: %s\n' "$final"
