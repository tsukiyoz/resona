#!/bin/sh
set -eu
umask 077

if [ "$#" -ne 1 ]; then
  echo 'Usage: activate.sh IMAGE (upgrade an existing data/access deployment)' >&2
  exit 1
fi
image=$1
case "$image" in ''|-*) echo 'Invalid image name' >&2; exit 1 ;; esac
root=${RESONA_ROOT:-/opt/resona}
container=${RESONA_CONTAINER:-resona-server}
port=${RESONA_PORT:-9988}
bind_ip=${RESONA_BIND_IP:-127.0.0.1}
wait_seconds=${RESONA_START_WAIT:-2}
case "$root" in /*) ;; *) echo 'RESONA_ROOT must be absolute' >&2; exit 1 ;; esac
case "$container" in ''|*[!A-Za-z0-9_.-]*) echo 'Invalid container name' >&2; exit 1 ;; esac
case "$port" in ''|*[!0-9]*) echo 'Invalid port' >&2; exit 1 ;; esac
if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then echo 'Invalid port' >&2; exit 1; fi
test -f "$root/data/noise.key"
test -d "$root/access"
docker image inspect "$image" >/dev/null
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true

# One upgrade per container; backups stay outside generated release packages.
lock="$root/.upgrade-$container.lock"
mkdir "$lock"
cleanup_lock() { rmdir "$lock"; }
trap cleanup_lock EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
mkdir -p "$root/backups"
backup_dir=$(mktemp -d "$root/backups/$container.XXXXXX")
backup="$container-backup-$(basename "$backup_dir")"
if docker container inspect "$backup" >/dev/null 2>&1; then
  echo 'Rollback container already exists' >&2
  exit 1
fi
docker inspect "$container" --format '{{range .Config.Env}}{{println .}}{{end}}' > "$backup_dir/environment"
rollback() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ -s "$backup_dir/new.cid" ]; then
    new_id=$(cat "$backup_dir/new.cid")
    docker rm -f "$new_id" >/dev/null 2>&1 || true
  fi
  if docker container inspect "$backup" >/dev/null 2>&1; then
    docker rename "$backup" "$container" || true
  fi
  docker start "$container" >/dev/null || echo 'Rollback failed; inspect the saved container and data backup' >&2
  cleanup_lock
  exit "$status"
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
docker stop -t 15 "$container" >/dev/null
tar -C "$root" -czf "$backup_dir/data-access.tar.gz" data access
docker rename "$container" "$backup"
docker run -d --name "$container" --cidfile "$backup_dir/new.cid" --restart unless-stopped \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --log-driver json-file --log-opt max-size=5m --log-opt max-file=2 \
  --env-file "$backup_dir/environment" \
  -p "$bind_ip:$port:9988/udp" -v "$root/data:/data:ro" -v "$root/access:/access" \
  "$image" --noise-key /data/noise.key --listen 0.0.0.0:9988 --access-dir /access >/dev/null
sleep "$wait_seconds"
test "$(docker inspect "$container" --format '{{.State.Running}}')" = true
trap - EXIT HUP INT TERM
cleanup_lock
echo "Started: $container"
echo "Rollback container: $backup"
echo "Private backup: $backup_dir"
