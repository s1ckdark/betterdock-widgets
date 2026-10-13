package market

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Summary struct {
	WidgetID      string   `json:"widgetId"`
	Downloads     int64    `json:"downloads"`
	RatingAverage *float64 `json:"ratingAverage"`
	RatingCount   int64    `json:"ratingCount"`
	ReviewCount   int64    `json:"reviewCount"`
}
type Review struct {
	ID        string    `json:"id"`
	Rating    int       `json:"rating"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	IsMine    bool      `json:"isMine"`
}
type PageCursor struct {
	Widget    string    `json:"widget"`
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}
type Store interface {
	Ping(context.Context) error
	HasCatalogVersion(context.Context, string, string) (bool, error)
	CreateInstallation(context.Context, []byte) error
	HasInstallation(context.Context, []byte) (bool, error)
	Summaries(context.Context, []string) ([]Summary, error)
	Reviews(context.Context, string, []byte, int, *PageCursor) ([]Review, *Review, error)
	PutReview(context.Context, string, []byte, int, string) (Review, error)
	DeleteReview(context.Context, string, []byte) error
	Download(context.Context, string, []byte, string) (bool, int64, error)
}
type PostgresStore struct{ Pool *pgxpool.Pool }

func (s PostgresStore) Ping(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `SELECT 1 FROM market_installations LIMIT 1`)
	return err
}
func (s PostgresStore) CreateInstallation(ctx context.Context, hash []byte) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO market_installations(token_hash) VALUES($1)`, hash)
	return err
}
func (s PostgresStore) HasInstallation(ctx context.Context, hash []byte) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM market_installations WHERE token_hash=$1)`, hash).Scan(&ok)
	return ok, err
}
func (s PostgresStore) Summaries(ctx context.Context, ids []string) ([]Summary, error) {
	rows, err := s.Pool.Query(ctx, `SELECT requested.id,
 (SELECT count(*) FROM market_downloads d WHERE d.widget_id=requested.id),
 (SELECT avg(r.rating)::float8 FROM market_reviews r WHERE r.widget_id=requested.id),
 (SELECT count(*) FROM market_reviews r WHERE r.widget_id=requested.id),
 (SELECT count(*) FROM market_reviews r WHERE r.widget_id=requested.id AND r.body <> '')
 FROM unnest($1::text[]) WITH ORDINALITY AS requested(id, ordinal) ORDER BY ordinal`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Summary, 0, len(ids))
	for rows.Next() {
		var v Summary
		if err := rows.Scan(&v.WidgetID, &v.Downloads, &v.RatingAverage, &v.RatingCount, &v.ReviewCount); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s PostgresStore) Reviews(ctx context.Context, widget string, hash []byte, limit int, cursor *PageCursor) ([]Review, *Review, error) {
	var before any
	var id any
	if cursor != nil {
		before = cursor.CreatedAt
		id = cursor.ID
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,rating,body,created_at,updated_at,COALESCE(installation_hash=$2,false) FROM market_reviews WHERE widget_id=$1 AND ($3::timestamptz IS NULL OR (created_at,id)<($3,$4::uuid)) ORDER BY created_at DESC,id DESC LIMIT $5`, widget, hash, before, id, limit)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Review, 0, limit)
	for rows.Next() {
		var r Review
		if err := rows.Scan(&r.ID, &r.Rating, &r.Body, &r.CreatedAt, &r.UpdatedAt, &r.IsMine); err != nil {
			rows.Close()
			return nil, nil, err
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	var mine *Review
	if len(hash) > 0 {
		var r Review
		err = s.Pool.QueryRow(ctx, `SELECT id::text,rating,body,created_at,updated_at,true FROM market_reviews WHERE widget_id=$1 AND installation_hash=$2`, widget, hash).Scan(&r.ID, &r.Rating, &r.Body, &r.CreatedAt, &r.UpdatedAt, &r.IsMine)
		if err == nil {
			mine = &r
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, err
		}
	}
	return out, mine, nil
}
func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:]), nil
}
func (s PostgresStore) PutReview(ctx context.Context, widget string, hash []byte, rating int, body string) (Review, error) {
	id, err := newUUID()
	if err != nil {
		return Review{}, err
	}
	var r Review
	err = s.Pool.QueryRow(ctx, `INSERT INTO market_reviews(id,installation_hash,widget_id,rating,body) VALUES($1,$2,$3,$4,$5) ON CONFLICT(installation_hash,widget_id) DO UPDATE SET rating=EXCLUDED.rating,body=EXCLUDED.body,updated_at=clock_timestamp() RETURNING id::text,rating,body,created_at,updated_at,true`, id, hash, widget, rating, body).Scan(&r.ID, &r.Rating, &r.Body, &r.CreatedAt, &r.UpdatedAt, &r.IsMine)
	return r, err
}
func (s PostgresStore) DeleteReview(ctx context.Context, widget string, hash []byte) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM market_reviews WHERE widget_id=$1 AND installation_hash=$2`, widget, hash)
	return err
}
func (s PostgresStore) Download(ctx context.Context, widget string, hash []byte, version string) (bool, int64, error) {
	result, err := s.Pool.Exec(ctx, `INSERT INTO market_downloads(installation_hash,widget_id,version) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, hash, widget, version)
	if err != nil {
		return false, 0, err
	}
	var count int64
	err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM market_downloads WHERE widget_id=$1`, widget).Scan(&count)
	return result.RowsAffected() > 0, count, err
}

// RecordCatalog is called only after LoadCatalog verifies the signed bytes. It
// never deletes old versions: offline receipts may arrive after a catalog update.
func (s PostgresStore) RecordCatalog(ctx context.Context, catalog Catalog) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for id, version := range catalog.Versions {
		if _, err := tx.Exec(ctx, `INSERT INTO market_catalog_versions(widget_id,version) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, version); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s PostgresStore) HasCatalogVersion(ctx context.Context, widget, version string) (bool, error) {
	var known bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM market_catalog_versions WHERE widget_id=$1 AND version=$2)`, widget, version).Scan(&known)
	return known, err
}
