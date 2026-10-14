#!/usr/bin/env python3
"""Repair imported campaign finished_at values from archived Sendy sent dates.

Run from the Postbox compose directory. Preview by default; pass --apply to
create a SQLite backup and write the repaired timestamps.
"""

import argparse
import datetime as dt
import pathlib
import sqlite3
import subprocess
import sys


DATABASE_NAME = "postbox.db"


def database_path() -> pathlib.Path:
    container = subprocess.check_output(
        ["docker", "compose", "ps", "-q", "postbox"], text=True
    ).strip()
    if not container:
        raise RuntimeError("Postbox container is not running; start it with docker compose up -d")
    mount = subprocess.check_output(
        [
            "docker", "inspect", "-f",
            "{{range .Mounts}}{{if eq .Destination \"/var/lib/postbox\"}}{{.Source}}{{end}}{{end}}",
            container,
        ],
        text=True,
    ).strip()
    if not mount:
        raise RuntimeError("could not find the Postbox data mount on the running container")
    path = pathlib.Path(mount) / DATABASE_NAME
    if not path.is_file():
        raise RuntimeError(f"Postbox database not found at {path}")
    return path


def candidates(db: sqlite3.Connection) -> list[tuple[int, int | None, str, str, int]]:
    rows = db.execute(
        """
        SELECT c.id, c.finished_at, mr.source, mr.source_id,
          CASE
            WHEN CAST(json_extract(mr.payload, '$.sent') AS INTEGER)
                 BETWEEN 1000000000 AND 253402300799
              THEN CAST(json_extract(mr.payload, '$.sent') AS INTEGER) * 1000
            WHEN CAST(json_extract(mr.payload, '$.sent') AS INTEGER)
                 BETWEEN 1000000000000 AND 253402300799999
              THEN CAST(json_extract(mr.payload, '$.sent') AS INTEGER)
            ELSE NULL
          END AS sent_at_ms
        FROM migration_identities AS mi
        JOIN migration_records AS mr
          ON mr.source = mi.source
         AND mr.entity = mi.entity
         AND mr.source_id = mi.source_id
        JOIN campaigns AS c ON c.id = mi.target_id
        WHERE mi.entity = 'campaigns'
          AND mr.entity = 'campaigns'
          AND c.status = 'sent'
        ORDER BY c.id
        """
    ).fetchall()
    return [(cid, old, source, source_id, sent) for cid, old, source, source_id, sent in rows if sent is not None]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true", help="back up the database and apply the repair")
    args = parser.parse_args()

    path = database_path()
    db = sqlite3.connect(path, timeout=30)
    try:
        rows = candidates(db)
        if not rows:
            print("No imported campaigns with a valid Sendy sent timestamp were found.")
            return 0

        print(f"{'Campaign':>9}  {'Current finished_at':>20}  {'Sendy sent date':>20}  Source")
        for campaign_id, old, source, source_id, sent_at in rows:
            rendered = dt.datetime.fromtimestamp(sent_at / 1000, dt.timezone.utc).isoformat()
            print(f"{campaign_id:>9}  {str(old):>20}  {rendered:>20}  {source}/{source_id}")
        print(f"\n{len(rows)} campaign(s) {'will be updated' if args.apply else 'would be updated'}.")
        if not args.apply:
            print("Preview only. Re-run with --apply to back up the database and apply these dates.")
            return 0

        stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        backup = path.with_name(f"{path.name}.before-sendy-date-fix-{stamp}")
        backup_db = sqlite3.connect(backup)
        try:
            db.backup(backup_db)
        finally:
            backup_db.close()

        db.execute("BEGIN IMMEDIATE")
        db.executemany(
            "UPDATE campaigns SET finished_at=? WHERE id=? AND status='sent'",
            [(sent_at, campaign_id) for campaign_id, _, _, _, sent_at in rows],
        )
        db.commit()
        print(f"Updated {len(rows)} campaign(s). Database backup: {backup}")
        return 0
    except Exception:
        db.rollback()
        raise
    finally:
        db.close()


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1)
