# RackNerd deployment

The API uses a dedicated `betterdock_market` database in the existing PostgreSQL 17 service.
`betterdock_market_owner` is a NOLOGIN schema owner; `betterdock_market_app` is a separate LOGIN
role with only the grants in `migrations/grants.sql.example`. Normal API startup never runs DDL.
Runtime credentials stay in `~/services/betterdock-market/runtime.env` on the server with mode 600.
They are not part of this repository or the application bundle.

The non-root container is named `betterdock-market-api`. It uses host networking to reach the
loopback PostgreSQL port, but the API itself binds only to the confirmed Tailscale address
`100.125.85.81:8787` (`LISTEN_ADDR`). Clients use `https://racknerd:8787` inside the same tailnet.
No public listener, Funnel, reverse proxy, or extra privileged Tailscale configuration is needed.
`TRUSTED_PROXY_CIDRS` is empty: requests arrive directly from tailnet peers, so forwarded client
IP headers are ignored.

Native TLS is enabled with `TLS_CERT_FILE=/tls/server.crt` and `TLS_KEY_FILE=/tls/server.key`.
Both paths must be set together. TLS 1.3 is required. The private EC key stays on RackNerd in
`~/services/betterdock-market/tls/server.key` with mode 600 and is mounted read-only. Run the
container as the same non-root UID that owns those files (`--user 1000:1000` in this deployment).
The filesystem is read-only, capabilities are dropped, and the signed index is mounted read-only.
Catalog files are public artifacts and must be readable by the container (mode 644).

The application pins the public server certificate and also validates its hostname and dates.
The current certificate covers `racknerd`, `racknerd.tail476516.ts.net`, and `100.125.85.81`.
The app does not change system trust or disable HTTPS checks for other services. When rotating
or renewing this certificate, distribute the new public pin in the app before replacing it;
never copy the private key into the application or this repository.

Readiness checks, using the public certificate as the trusted anchor (never `curl -k`):

```sh
curl --cacert ~/services/betterdock-market/tls/server.crt https://racknerd:8787/healthz
curl --cacert ~/services/betterdock-market/tls/server.crt 'https://racknerd:8787/v1/widgets?ids=com.betterdock.clock'
```

Keep existing Tailscale Serve routes intact. API availability requires joining the same tailnet;
widget catalog and package downloads remain available from the public signed GitHub repository.
The signed catalog is loaded at startup, and verified versions are retained for delayed
installation receipts after future catalog updates.

For an update, validate and sign the market index, build the API image, run PostgreSQL tests,
apply any additive schema migration as the owner, then replace only this container. Keep the
previous image and release directory for rollback. Never rotate the runtime database password
or delete installation/review/download rows as part of a routine release. Catalog renewal must
also redeploy its signed index to the server.

Anonymous identity limits: counts are deduplicated by installation + widget + version, not
verified people. Migration of existing bundled widgets and app upgrades are excluded. Reviews
are optional public user text; clients render them as plain text, and per-installation/IP limits
bound writes. No device name, email, provider key, or widget setting is part of this API.
