#!/bin/sh
# Starts the service, then (if SEED_DEMO=1) loads demo/fixture.json once it is healthy.
# State is in memory, so every restart starts from the same demo data.
set -u

/app/pocketful &
pid=$!
trap 'kill -TERM "$pid" 2>/dev/null' TERM INT

if [ "${SEED_DEMO:-1}" = "1" ]; then
  base="http://127.0.0.1:${PORT}"
  i=0
  until wget -qO- "$base/health" >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -gt 60 ]; then echo "service not healthy after 60s, skipping demo data"; break; fi
    sleep 1
  done
  if wget -qO- --header 'Content-Type: application/json' \
       --post-file /app/demo/fixture.json "$base/_test/reset" >/dev/null 2>&1; then
    echo "demo data loaded"
  else
    echo "demo data failed to load"
  fi
fi

wait "$pid"
