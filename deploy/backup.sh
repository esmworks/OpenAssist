#!/bin/sh
# Postgres yedekleme (Faz 9, PRD §6): günlük dump + saklama.
# Kullanım (sunucuda, compose dizininden):
#   ./backup.sh                 # bir kez yedekle
# Cron (Dokploy host'unda, her gece 03:15):
#   15 3 * * * cd /path/to/openassist/deploy && ./backup.sh >> ../files/backups/backup.log 2>&1
#
# Saklama: son 14 günlük dump tutulur; geri yükleme:
#   gunzip -c ../files/backups/openassist-YYYYMMDD.sql.gz | \
#     docker compose exec -T db psql -U openassist -d openassist
set -eu

BACKUP_DIR="${BACKUP_DIR:-../files/backups}"
KEEP_DAYS="${KEEP_DAYS:-14}"
STAMP="$(date +%Y%m%d-%H%M%S)"

mkdir -p "$BACKUP_DIR"
docker compose exec -T db pg_dump -U openassist -d openassist \
  | gzip > "$BACKUP_DIR/openassist-$STAMP.sql.gz"

# Bayat yedekleri buda.
find "$BACKUP_DIR" -name 'openassist-*.sql.gz' -mtime +"$KEEP_DAYS" -delete

echo "yedek alındı: $BACKUP_DIR/openassist-$STAMP.sql.gz"
