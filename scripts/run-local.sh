#!/bin/sh
set -eu

AWS_ACCESS_KEY_ID=fake \
AWS_SECRET_ACCESS_KEY=fake \
AWS_ENDPOINT_URL=http://127.0.0.1:9001 \
exec ./bin/postbox serve \
  --db /tmp/postbox/postbox.db \
  --listen 127.0.0.1:8080 \
  --base-url http://localhost:8080 \
  --region eu-west-1 \
  --development \
  --secure-cookies=false \
  --backup-dir /tmp/postbox/backups
