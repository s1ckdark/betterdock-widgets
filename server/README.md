# BetterDock market community API

This service stores live community data in PostgreSQL. Widget packages and their
Ed25519-signed catalog stay in the parent repository; ratings/download counts are
never signed into that static catalog.

## Run and deploy

Requires Go 1.27 and PostgreSQL 17. Build from this directory:

```sh
go build -trimpath -o market-api ./cmd/market-api
docker build -t betterdock-market .
```

The operator creates an isolated `betterdock_market` database, a NOLOGIN owner
role, and a separate LOGIN `betterdock_market_app` role. As that database's owner,
apply `migrations/001_market.sql` and then the grants in
`migrations/grants.sql.example`. The normal process does **not** perform DDL or
migrate any database. Runtime only needs SELECT/INSERT on installations/downloads/verified catalog versions
and SELECT/INSERT/UPDATE/DELETE on reviews. Tables retain data across app and API
restarts. Back up this database with the server's normal PostgreSQL backup plan.
Do not store a real connection string or token in this repository.

| Environment | Meaning |
| --- | --- |
| `DATABASE_URL` | Required PostgreSQL DSN for the restricted app role. Supply through a protected env file or secret manager. |
| `LISTEN_ADDR` | Default `:8080`; private deployment binds only the Tailscale IP, e.g. `100.125.85.81:8787`, with native TLS enabled. Loopback tests may use `127.0.0.1:8787` without TLS. |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | Optional native HTTPS certificate/key PEM paths. Set both or neither. Both enable TLS 1.3 minimum; a missing/unreadable/mismatched pair aborts startup. Mount read-only and readable only by the runtime user. |
| `CATALOG_PATH` | Default `/catalog/index.json`; mount the signed catalog read-only. |
| `CATALOG_SIGNATURE_PATH` | Default `/catalog/index.json.sig`; mount detached base64 Ed25519 signature read-only. |
| `TRUSTED_PROXY_CIDRS` | Comma-separated trusted immediate/forwarding proxy CIDRs, default none. For a local proxy use only `127.0.0.1/32,::1/128`. Never trust the Internet or a whole client subnet. |

The community API is private to the Tailscale network: use native HTTPS bound
only to the node's Tailscale IP and `https://racknerd:8787`, with a certificate
whose DNS SAN contains `racknerd`. The desktop verifies the actual TLS peer,
hostname, and pinned public certificate. Never expose it through Funnel or a
public listener. The bearer token grants control of that installation's reviews.
Keep the private key on the server; distribute only the public certificate.
Certificates are loaded once at startup, so restart after rotation and coordinate
any client pin update. Plain HTTP is for loopback tests/private proxy plumbing
only. Mount the catalog without granting write access. Container
runs as a non-root user. It works with `--read-only --cap-drop ALL
--security-opt no-new-privileges`; the image doesn't write runtime files.

Startup verifies the exact catalog bytes against the same embedded Ed25519
public key as BetterDock. Bad signatures or invalid IDs/versions stop startup.
Only signed IDs are accepted. Downloads only accept versions recorded from a signature-verified catalog. Expiry does not remove historical community data; the desktop
installer independently refuses expired catalogs. A newly signed catalog is
loaded on API restart/redeploy; deploy it alongside catalog updates. Verified widget/version pairs are persisted at startup (no DDL), so offline
receipts for an older signed version remain valid after a catalog replacement.
Historical versions not previously ingested cannot be inferred from client claims.
The widget ID must still be present in the current signed catalog.

`GET /healthz` checks access to the installation table, returns `{"status":"ok"}`
or HTTP 503. TLS proxy access logs must omit Authorization headers, request bodies
and query credentials; the API itself does not log tokens, IPs, bodies or DSNs.
Startup failures intentionally use generic messages without database secrets.

## API contract

All successful JSON responses use UTF-8. Timestamps are ISO 8601 UTC (fractional
seconds may be present). Responses are `Cache-Control: no-store`, including
anonymous responses. Public reads omit Authorization. Authenticated reads and all
review/download writes use `Authorization: Bearer <token>`. If a supplied token
is invalid, even on a read, the response is 401. All errors are non-2xx; database
failures are **503**, never an invented zero rating or download count.

- `POST /v1/installations` (empty body, no Authorization) → 201 `{"token":"…"}`.
  A 32-byte cryptographically random token encoded as 43 base64url characters,
  returned once; only SHA-256 of the decoded token bytes is persisted. Desktop
  stores it in its own Keychain and must never generate a replacement on transient
  network/Keychain errors. Losing the token loses the ability to edit the review.
- `GET /v1/widgets?ids=<comma-separated IDs>` → 200
  `{"widgets":[{"widgetId":"…","downloads":0,"ratingAverage":null,"ratingCount":0,"reviewCount":0}],"asOf":"…"}`.
  Requires 1–100 unique known IDs. IDs remain in requested order. No ratings means
  null average, and a real empty database means zero counts.
- `GET /v1/widgets/<id>/reviews?limit=20&cursor=<opaque>` → 200
  `{"reviews":[{"id":"uuid","rating":5,"body":"…","createdAt":"…","updatedAt":"…","isMine":false}],"nextCursor":null,"mine":null}`.
  Limit 1–100, default 20. Newest immutable creation time/id first. Use returned
  cursor as-is; cursors are scoped to a widget. `mine` is the authenticated
  installation's review even when it is outside the page. Public review entries
  expose no installation identity or credential. Stars-only entries have empty
  body, count toward `ratingCount`, and do not count toward `reviewCount`.
- `PUT /v1/widgets/<id>/review`, JSON `{"rating":1,"body":"…"}` → 200
  `{"review":<review with isMine true>,"summary":<widget summary>}`.
  Integer rating 1–5; body required, at most 2000 Unicode code points. Empty or
  whitespace-only body is a stars-only rating. Leading/trailing whitespace is
  trimmed after length validation. Control characters other than LF/CR/TAB are
  rejected. Repeated writes update the same review/creation time; no extra vote.
- `DELETE /v1/widgets/<id>/review` (empty body) → 200
  `{"deleted":true,"summary":<widget summary>}`. Deletes only the calling
  installation's review. Deleting an already absent review succeeds.
- `POST /v1/widgets/<id>/downloads`, JSON `{"version":"1.2.3"}` → 200
  `{"counted":true,"downloads":1}`. Client sends only after an actual successful
  market install/update. Migration of an existing bundled widget and a desktop
  app update must not send a receipt. Unique `(installation, widget, version)`
  enforces idempotency under concurrent requests and across restarts. Reinstalling
  the same version returns counted false; a new version may count once.

Writes with JSON require `Content-Type: application/json`. Unknown or duplicate JSON fields, bodies over 32 KiB,
trailing JSON, invalid UTF-8, invalid numeric ratings and malformed query strings
are rejected. Error payload: `{"error":{"code":"…","message":"…"}}`. Important
status codes: 400 bad input/version/cursor, 401 invalid identity, 404 unsigned
widget, 405 unsupported method, 413 oversized body, 415 content type, 429 rate
limit (Retry-After), 503 temporary data unavailability. Router 404/405 responses
may be plain text; clients should use status first, payload only when decodable.

## Anonymous abuse and privacy limits

This is **one review per installation identity**, not per verified person. No
login, email, device fingerprint or IP is stored in the database. A client can
claim an install; this is not cryptographic proof that files were installed.
The counts therefore represent accepted installation/version receipts, not
verified unique people, raw package HTTP requests, or active users. Clearing the
Keychain or deliberately creating identities can increase counts. Reviews are
plain user text; native clients should display text without HTML execution.
There is no moderation console in this first version; the operator can remove
abusive reviews using the owner role with normal audit/change procedures.

Rate limits are in-memory and bounded to 50,000 keys: 240 requests/minute/IP,
60 writes/minute/IP, 5 installation registrations/hour/IP, and 120 authenticated
requests/minute/token. IPv6 clients share a /64 limit. Proxied addresses are only
used through explicitly trusted proxy hops (right-to-left X-Forwarded-For);
otherwise the TCP peer is used. Unknown/invalid forwarding falls back safely.
Counters reset when this **single-replica** service restarts. NAT users share IP
limits. Multiple replicas require a shared limiter before deployment. The limits
reduce casual abuse, not determined Sybil attacks. A 429 response uses a minimum
60-second retry hint; registration limits can require waiting up to an hour.

## Tests

```sh
go test ./...                  # unit tests; integration explicitly skipped
go vet ./...
scripts/test-postgres.sh       # disposable loopback PostgreSQL 17 + go test -race
```

The script starts a uniquely named `postgres:17-alpine` container, creates fresh
schemas and a restricted runtime role, and deletes only its own resources. It
never reads a production DSN. Explicit `TEST_DATABASE_URL` integration runs are
restricted to loopback and database name `betterdock_market_test`. Tests cover
signature tampering, unknown IDs/versions, request limits, proxy trust, data-error
responses, real SQL averages, Unicode bounds, ownership, pagination, concurrent
upserts and idempotent download receipts. Automated tests do not prove production
proxy configuration, long-term abuse resistance, or a real desktop install flow.

Implementation references: Go's [net/http](https://pkg.go.dev/net/http) server and
[pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool) documentation.
