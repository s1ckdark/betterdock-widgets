-- Run once as the database/schema owner, never as the API role.
BEGIN;
CREATE TABLE IF NOT EXISTS market_installations (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS market_reviews (
    id uuid PRIMARY KEY,
    installation_hash bytea NOT NULL REFERENCES market_installations(token_hash),
    widget_id text NOT NULL CHECK (widget_id ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    rating smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    body text NOT NULL CHECK (char_length(body) <= 2000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (installation_hash, widget_id)
);
CREATE INDEX IF NOT EXISTS market_reviews_page ON market_reviews (widget_id, created_at DESC, id DESC);
-- Versions only enter this table after successful Ed25519 catalog verification.
-- Retain them so delayed receipts survive a catalog update.
CREATE TABLE IF NOT EXISTS market_catalog_versions (
    widget_id text NOT NULL CHECK (widget_id ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    version text NOT NULL CHECK (version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'),
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (widget_id, version)
);
CREATE TABLE IF NOT EXISTS market_downloads (
    installation_hash bytea NOT NULL REFERENCES market_installations(token_hash),
    widget_id text NOT NULL CHECK (widget_id ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    version text NOT NULL CHECK (version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_hash, widget_id, version),
    FOREIGN KEY (widget_id, version) REFERENCES market_catalog_versions(widget_id, version)
);
CREATE INDEX IF NOT EXISTS market_downloads_widget ON market_downloads (widget_id);
COMMIT;
