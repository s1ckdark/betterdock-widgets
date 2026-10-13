package market

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationStore(t *testing.T) PostgresStore {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL or use scripts/test-postgres.sh for isolated PostgreSQL 17 integration")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	// Deliberately reject remote/ordinary database names, including a copied production DSN.
	if (config.ConnConfig.Host != "127.0.0.1" && config.ConnConfig.Host != "localhost" && config.ConnConfig.Host != "::1") || config.ConnConfig.Database != "betterdock_market_test" {
		t.Fatal("integration tests require loopback betterdock_market_test database")
	}
	ctx := context.Background()
	owner, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	name := "market_test_" + hex.EncodeToString(suffix)
	role := name + "_app"
	if _, err := owner.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		owner.Close()
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = owner.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE")
		_, _ = owner.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
		owner.Close()
	})
	setupConfig := config.Copy()
	setupConfig.ConnConfig.RuntimeParams["search_path"] = name
	setup, err := pgxpool.NewWithConfig(ctx, setupConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close()
	sql, err := os.ReadFile("../../migrations/001_market.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = setup.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	grants := fmt.Sprintf(`GRANT USAGE ON SCHEMA %s TO %s; GRANT SELECT,INSERT ON market_installations,market_downloads,market_catalog_versions TO %s; GRANT SELECT,INSERT,UPDATE,DELETE ON market_reviews TO %s`, pgx.Identifier{name}.Sanitize(), pgx.Identifier{role}.Sanitize(), pgx.Identifier{role}.Sanitize(), pgx.Identifier{role}.Sanitize())
	if _, err := setup.Exec(ctx, grants); err != nil {
		t.Fatal(err)
	}
	appConfig := setupConfig.Copy()
	appConfig.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, appConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "CREATE TABLE forbidden(id int)"); err == nil {
		t.Fatal("runtime role unexpectedly has DDL")
	}
	store := PostgresStore{Pool: pool}
	if err := store.RecordCatalog(ctx, testCatalog()); err != nil {
		t.Fatal(err)
	}
	return store
}
func decodeResponse(t *testing.T, w interface{ Bytes() []byte }, target any) {
	t.Helper()
	if err := json.Unmarshal(w.Bytes(), target); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
func registerToken(t *testing.T, h http.Handler) string {
	t.Helper()
	w := request(h, "POST", "/v1/installations", "", "")
	if w.Code != 201 {
		t.Fatalf("registration: %d %s", w.Code, w.Body)
	}
	var response struct {
		Token string `json:"token"`
	}
	decodeResponse(t, w.Body, &response)
	return response.Token
}
func tokenHash(t *testing.T, token string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 {
		t.Fatal("invalid issued token")
	}
	hash := sha256.Sum256(b)
	return hash[:]
}
func readSummary(t *testing.T, h http.Handler) Summary {
	t.Helper()
	w := request(h, "GET", "/v1/widgets?ids=com.betterdock.clock", "", "")
	if w.Code != 200 {
		t.Fatalf("summary: %s", w.Body)
	}
	var response struct {
		Widgets []Summary `json:"widgets"`
	}
	decodeResponse(t, w.Body, &response)
	return response.Widgets[0]
}
func TestPostgresAPIEndToEnd(t *testing.T) {
	store := integrationStore(t)
	api := NewAPI(store, testCatalog(), nil)
	h := api.Handler()
	if request(h, "GET", "/healthz", "", "").Code != 200 {
		t.Fatal("health failed")
	}
	summary := readSummary(t, h)
	if summary.Downloads != 0 || summary.RatingAverage != nil || summary.RatingCount != 0 || summary.ReviewCount != 0 {
		t.Fatalf("new widget stats: %+v", summary)
	}
	first := registerToken(t, h)
	second := registerToken(t, h)
	if first == second || len(first) != 43 {
		t.Fatal("identity tokens not independently random")
	}
	hash := tokenHash(t, first)
	var storedLength int
	if err := store.Pool.QueryRow(context.Background(), `SELECT octet_length(token_hash) FROM market_installations WHERE token_hash=$1`, hash).Scan(&storedLength); err != nil || storedLength != 32 {
		t.Fatal("hash was not persisted")
	}
	t.Run("review validation", func(t *testing.T) {
		for _, body := range []string{`{"rating":0,"body":"x"}`, `{"rating":6,"body":"x"}`, `{"rating":1.5,"body":"x"}`, `{"rating":5}`, `{"rating":5,"body":null}`, `{"rating":5,"body":"\u0000"}`, `{"rating":5,"body":"\u0001"}`, `{"rating":5,"body":"x","other":1}`, `{"rating":5,"body":"x"} {}`} {
			w := request(h, "PUT", "/v1/widgets/com.betterdock.clock/review", first, body)
			if w.Code != 400 {
				t.Errorf("accepted invalid review: %d %s", w.Code, w.Body)
			}
		}
		oversized, _ := json.Marshal(map[string]any{"rating": 5, "body": strings.Repeat("한", 2001)})
		if request(h, "PUT", "/v1/widgets/com.betterdock.clock/review", first, string(oversized)).Code != 400 {
			t.Fatal("unicode review bound not enforced")
		}
	})
	body, _ := json.Marshal(map[string]any{"rating": 5, "body": strings.Repeat("한", 2000)})
	w := request(h, "PUT", "/v1/widgets/com.betterdock.clock/review", first, string(body))
	if w.Code != 200 {
		t.Fatalf("2000 Unicode accepted: %s", w.Body)
	}
	var initial struct {
		Review Review `json:"review"`
	}
	decodeResponse(t, w.Body, &initial)
	w = request(h, "PUT", "/v1/widgets/com.betterdock.clock/review", second, `{"rating":3,"body":""}`)
	if w.Code != 200 {
		t.Fatalf("stars-only: %s", w.Body)
	}
	summary = readSummary(t, h)
	if summary.RatingAverage == nil || *summary.RatingAverage != 4 || summary.RatingCount != 2 || summary.ReviewCount != 1 {
		t.Fatalf("initial aggregates: %+v", summary)
	}
	w = request(h, "PUT", "/v1/widgets/com.betterdock.clock/review", first, `{"rating":1,"body":"edited"}`)
	var edited struct {
		Review Review `json:"review"`
	}
	decodeResponse(t, w.Body, &edited)
	if w.Code != 200 || edited.Review.ID != initial.Review.ID || !edited.Review.CreatedAt.Equal(initial.Review.CreatedAt) || !edited.Review.IsMine {
		t.Fatalf("upsert failed: %s", w.Body)
	}
	summary = readSummary(t, h)
	if *summary.RatingAverage != 2 || summary.RatingCount != 2 {
		t.Fatal("edit added vote instead of updating")
	}
	t.Run("identity and pagination", func(t *testing.T) {
		w := request(h, "GET", "/v1/widgets/com.betterdock.clock/reviews?limit=1", first, "")
		var page struct {
			Reviews []Review `json:"reviews"`
			Mine    *Review  `json:"mine"`
			Next    *string  `json:"nextCursor"`
		}
		decodeResponse(t, w.Body, &page)
		if w.Code != 200 || len(page.Reviews) != 1 || page.Mine == nil || page.Mine.ID != initial.Review.ID || !page.Mine.IsMine || page.Next == nil {
			t.Fatalf("mine/page: %s", w.Body)
		}
		firstID := page.Reviews[0].ID
		w = request(h, "GET", "/v1/widgets/com.betterdock.clock/reviews?limit=1&cursor="+*page.Next, first, "")
		page.Next = nil
		decodeResponse(t, w.Body, &page)
		if len(page.Reviews) != 1 || page.Reviews[0].ID == firstID || page.Next != nil {
			t.Fatalf("pagination duplicates: %s", w.Body)
		}
		w = request(h, "GET", "/v1/widgets/com.betterdock.clock/reviews", "", "")
		decodeResponse(t, w.Body, &page)
		if page.Mine != nil {
			t.Fatal("anonymous request leaked mine")
		}
		for _, r := range page.Reviews {
			if r.IsMine {
				t.Fatal("anonymous request marked another review mine")
			}
		}
		unknown := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		if request(h, "GET", "/v1/widgets/com.betterdock.clock/reviews", unknown, "").Code != 401 {
			t.Fatal("unknown token accepted")
		}
	})
	t.Run("download idempotency and concurrency", func(t *testing.T) {
		if request(h, "POST", "/v1/widgets/com.betterdock.clock/downloads", first, `{"version":"9.9.9"}`).Code != 400 {
			t.Fatal("unsigned version accepted")
		}
		var counted atomic.Int32
		var failures atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := request(h, "POST", "/v1/widgets/com.betterdock.clock/downloads", first, `{"version":"1.0.0"}`)
				var result struct {
					Counted   bool  `json:"counted"`
					Downloads int64 `json:"downloads"`
				}
				if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || result.Downloads != 1 {
					failures.Add(1)
				}
				if result.Counted {
					counted.Add(1)
				}
			}()
		}
		wg.Wait()
		if counted.Load() != 1 || failures.Load() != 0 {
			t.Fatalf("concurrent retry: counted=%d failures=%d", counted.Load(), failures.Load())
		}
		// New API/store instance shares persistent rows, so a restart cannot recount.
		newHandler := NewAPI(PostgresStore{Pool: store.Pool}, testCatalog(), nil).Handler()
		w := request(newHandler, "POST", "/v1/widgets/com.betterdock.clock/downloads", first, `{"version":"1.0.0"}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"counted":false`) {
			t.Fatalf("restart recounted: %s", w.Body)
		}
		w = request(newHandler, "POST", "/v1/widgets/com.betterdock.clock/downloads", second, `{"version":"1.0.0"}`)
		if w.Code != 200 || readSummary(t, newHandler).Downloads != 2 {
			t.Fatal("independent installation not counted")
		}
	})
	t.Run("delete own review only", func(t *testing.T) {
		// Fresh limiter isolates this assertion from the deliberate request bursts above.
		h := NewAPI(store, testCatalog(), nil).Handler()
		if request(h, "DELETE", "/v1/widgets/com.betterdock.clock/review", first, "").Code != 200 {
			t.Fatal("delete failed")
		}
		summary := readSummary(t, h)
		if summary.RatingCount != 1 || summary.ReviewCount != 0 || *summary.RatingAverage != 3 {
			t.Fatalf("delete touched another review: %+v", summary)
		}
		if request(h, "DELETE", "/v1/widgets/com.betterdock.clock/review", first, "").Code != 200 {
			t.Fatal("delete retry not idempotent")
		}
	})
}
func TestPostgresConcurrentReviewUpsert(t *testing.T) {
	store := integrationStore(t)
	ctx := context.Background()
	hash := sha256.Sum256([]byte("isolated-test-identity"))
	if err := store.CreateInstallation(ctx, hash[:]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(rating int) {
			defer wg.Done()
			_, err := store.PutReview(ctx, "example-uptime", hash[:], rating, "concurrent edit")
			errors <- err
		}(i%5 + 1)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.Summaries(ctx, []string{"example-uptime"})
	if err != nil || summary[0].RatingCount != 1 || summary[0].ReviewCount != 1 {
		t.Fatalf("upsert uniqueness failed: %+v %v", summary, err)
	}
	rows, mine, err := store.Reviews(ctx, "example-uptime", hash[:], 20, nil)
	if err != nil || len(rows) != 1 || mine == nil || !mine.IsMine {
		t.Fatal("concurrent upserts lost identity")
	}
}

func TestPostgresDelayedDownloadSurvivesCatalogUpdate(t *testing.T) {
	store := integrationStore(t)
	initial := testCatalog()
	h := NewAPI(store, initial, nil).Handler()
	token := registerToken(t, h)
	updated := testCatalog()
	updated.Versions["com.betterdock.clock"] = "2.0.0"
	updated.Sequence = 3
	if err := store.RecordCatalog(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	h = NewAPI(store, updated, nil).Handler()
	for _, version := range []string{"1.0.0", "2.0.0"} {
		w := request(h, "POST", "/v1/widgets/com.betterdock.clock/downloads", token, `{"version":"`+version+`"}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"counted":true`) {
			t.Fatalf("verified historical/current version rejected: %s", w.Body)
		}
	}
	if request(h, "POST", "/v1/widgets/com.betterdock.clock/downloads", token, `{"version":"9.9.9"}`).Code != 400 {
		t.Fatal("unseen version accepted")
	}
	if readSummary(t, h).Downloads != 2 {
		t.Fatal("delayed version not counted")
	}
}
