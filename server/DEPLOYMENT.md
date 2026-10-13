# RackNerd deployment

The API uses a dedicated `betterdock_market` database in the existing PostgreSQL 17 service.
`betterdock_market_owner` is a NOLOGIN schema owner; `betterdock_market_app` is a separate LOGIN
role with only the grants in `migrations/grants.sql.example`. Normal API startup never runs DDL.
Runtime credentials stay in `~/services/betterdock-market/runtime.env` on the server with mode 600.
They are not part of this repository or the application bundle.

The non-root container is named `betterdock-market-api`. It uses host networking to reach the
loopback PostgreSQL port and listens only on `127.0.0.1:8787`. Its filesystem is read-only,
capabilities are dropped, and the catalog index and signature are mounted read-only.
The public catalog files must be readable by the container user (mode 644); credential files
remain private. The signed catalog is loaded at startup, and its verified versions are recorded
for accepting delayed installation receipts after future catalog updates.

Private readiness check:

```sh
curl --fail http://127.0.0.1:8787/healthz
```

An administrator enables the dedicated HTTPS endpoint with:

```sh
sudo tailscale funnel --bg --https=8443 http://127.0.0.1:8787
```

This maps `https://racknerd.tail476516.ts.net:8443` to this API only. Keep existing Tailscale
Serve routes intact; do not use `tailscale funnel reset`. Configure `TRUSTED_PROXY_CIDRS` only
for the immediate proxy peers (`127.0.0.1/32,::1/128` for this deployment). Verify the public
`/healthz` and `/v1/widgets?ids=com.betterdock.clock` endpoints after enabling it.

For an update, validate and sign the market index, build the API image, run PostgreSQL tests,
apply any additive schema migration as the owner, then replace only this container. Keep the
previous image and release directory for rollback. Never rotate the runtime database password
or delete installation/review/download rows as part of a routine release. Catalog renewal must
also redeploy its signed index to the server.

Anonymous identity limits: counts are deduplicated by installation + widget + version, not
verified people. Migration of existing bundled widgets and app upgrades are excluded. Reviews
are optional public user text; clients render them as plain text, and per-installation/IP limits
bound writes. No device name, email, provider key, or widget setting is part of this API.
