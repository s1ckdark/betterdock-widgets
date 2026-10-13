#!/bin/sh
# A disposable, loopback-only PostgreSQL 17. No production DSN is used.
set -eu
cd "$(dirname "$0")/.."
container="betterdock-market-test-$$"
docker run -d --rm --name "$container" -e POSTGRES_USER=market_test -e POSTGRES_PASSWORD=local_test_only -e POSTGRES_DB=betterdock_market_test -p 127.0.0.1::5432 postgres:17-alpine >/dev/null
trap 'docker stop "$container" >/dev/null' EXIT INT TERM
tries=0
until docker exec "$container" pg_isready -U market_test -d betterdock_market_test >/dev/null 2>&1; do
    tries=$((tries + 1))
    if [ "$tries" -ge 60 ]; then echo 'Test PostgreSQL did not become ready' >&2; exit 1; fi
    sleep 1
done
port=$(docker port "$container" 5432/tcp | sed 's/.*://')
TEST_DATABASE_URL="postgres://market_test:local_test_only@127.0.0.1:${port}/betterdock_market_test?sslmode=disable" go test -race -count=1 ./...
