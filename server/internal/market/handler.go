package market

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type API struct {
	Store          Store
	Catalog        Catalog
	Limiter        *Limiter
	TrustedProxies []netip.Prefix
	Now            func() time.Time
}

func NewAPI(store Store, catalog Catalog, proxies []netip.Prefix) *API {
	return &API{Store: store, Catalog: catalog, Limiter: NewLimiter(), TrustedProxies: proxies, Now: time.Now}
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /v1/installations", a.register)
	mux.HandleFunc("GET /v1/widgets", a.summaries)
	mux.HandleFunc("GET /v1/widgets/{id}/reviews", a.reviews)
	mux.HandleFunc("PUT /v1/widgets/{id}/review", a.putReview)
	mux.HandleFunc("DELETE /v1/widgets/{id}/review", a.deleteReview)
	mux.HandleFunc("POST /v1/widgets/{id}/downloads", a.download)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path != "/healthz" {
			ip := clientIP(r.RemoteAddr, r.Header.Get("X-Forwarded-For"), a.TrustedProxies)
			if !a.Limiter.Allow("ip:"+ip, 240, time.Minute, a.Now()) {
				rateLimited(w)
				return
			}
			if r.Method != "GET" && r.Method != "HEAD" && !a.Limiter.Allow("write:"+ip, 60, time.Minute, a.Now()) {
				rateLimited(w)
				return
			}
			if r.URL.Path == "/v1/installations" && !a.Limiter.Allow("register:"+ip, 5, time.Hour, a.Now()) {
				rateLimited(w)
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func unavailable(w http.ResponseWriter) {
	fail(w, 503, "unavailable", "Community data is temporarily unavailable.")
}
func rateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	fail(w, 429, "rate_limited", "Please try again later.")
}
func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if a.Store.Ping(r.Context()) != nil {
		unavailable(w)
		return
	}
	respond(w, 200, map[string]string{"status": "ok"})
}
func (a *API) register(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Authorization")) > 0 || r.URL.RawQuery != "" {
		fail(w, 400, "invalid_request", "Registration does not accept authorization or query parameters.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16))
	if err != nil || len(bytes.TrimSpace(raw)) != 0 {
		fail(w, 400, "invalid_request", "Registration requires an empty body.")
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		unavailable(w)
		return
	}
	hash := sha256.Sum256(token)
	if a.Store.CreateInstallation(r.Context(), hash[:]) != nil {
		unavailable(w)
		return
	}
	respond(w, 201, map[string]string{"token": base64.RawURLEncoding.EncodeToString(token)})
}
func (a *API) auth(w http.ResponseWriter, r *http.Request, required bool) ([]byte, bool) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 && !required {
		return nil, true
	}
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		fail(w, 401, "unauthorized", "A valid installation token is required.")
		return nil, false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != token {
		fail(w, 401, "unauthorized", "A valid installation token is required.")
		return nil, false
	}
	h := sha256.Sum256(b)
	// Count token attempts before the DB lookup, including nonexistent identities.
	if !a.Limiter.Allow("token:"+hex.EncodeToString(h[:]), 120, time.Minute, a.Now()) {
		rateLimited(w)
		return nil, false
	}
	ok, err := a.Store.HasInstallation(r.Context(), h[:])
	if err != nil {
		unavailable(w)
		return nil, false
	}
	if !ok {
		fail(w, 401, "unauthorized", "A valid installation token is required.")
		return nil, false
	}
	return h[:], true
}
func (a *API) widget(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, ok := a.Catalog.Versions[id]; !ok {
		fail(w, 404, "unknown_widget", "Widget is not in the signed catalog.")
		return "", false
	}
	return id, true
}
func urlQuery(r *http.Request) (url.Values, error) { return url.ParseQuery(r.URL.RawQuery) }
func onlyQuery(r *http.Request, allowed ...string) bool {
	q, err := urlQuery(r)
	if err != nil {
		return false
	}
	for key, values := range q {
		found := false
		for _, v := range allowed {
			if key == v {
				found = true
			}
		}
		if !found || len(values) != 1 {
			return false
		}
	}
	return true
}
func (a *API) summaries(w http.ResponseWriter, r *http.Request) {
	if !onlyQuery(r, "ids") {
		fail(w, 400, "invalid_query", "Only a single ids parameter is accepted.")
		return
	}
	if _, ok := a.auth(w, r, false); !ok {
		return
	}
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(ids) > 100 || len(ids) == 0 {
		fail(w, 400, "invalid_ids", "Provide 1 to 100 catalog widget IDs.")
		return
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := a.Catalog.Versions[id]; !ok {
			fail(w, 404, "unknown_widget", "Widget is not in the signed catalog.")
			return
		}
		if seen[id] {
			fail(w, 400, "invalid_ids", "Widget IDs must be unique.")
			return
		}
		seen[id] = true
	}
	summaries, err := a.Store.Summaries(r.Context(), ids)
	if err != nil {
		unavailable(w)
		return
	}
	respond(w, 200, map[string]any{"widgets": summaries, "asOf": a.Now().UTC()})
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (a *API) reviews(w http.ResponseWriter, r *http.Request) {
	widget, ok := a.widget(w, r)
	if !ok {
		return
	}
	if !onlyQuery(r, "limit", "cursor") {
		fail(w, 400, "invalid_query", "Invalid review query.")
		return
	}
	hash, ok := a.auth(w, r, false)
	if !ok {
		return
	}
	limit := 20
	if v, exists := r.URL.Query()["limit"]; exists {
		n, err := strconv.Atoi(v[0])
		if err != nil || n < 1 || n > 100 {
			fail(w, 400, "invalid_limit", "Limit must be 1 to 100.")
			return
		}
		limit = n
	}
	var cursor *PageCursor
	if value, exists := r.URL.Query()["cursor"]; exists {
		raw, err := base64.RawURLEncoding.DecodeString(value[0])
		var c PageCursor
		if err != nil || len(raw) > 512 || decodeStrict(raw, &c) != nil || c.Widget != widget || c.CreatedAt.IsZero() || !uuidPattern.MatchString(c.ID) {
			fail(w, 400, "invalid_cursor", "Review cursor is invalid.")
			return
		}
		cursor = &c
	}
	rows, mine, err := a.Store.Reviews(r.Context(), widget, hash, limit+1, cursor)
	if err != nil {
		unavailable(w)
		return
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		data, _ := json.Marshal(PageCursor{Widget: widget, CreatedAt: last.CreatedAt, ID: last.ID})
		encoded := base64.RawURLEncoding.EncodeToString(data)
		next = &encoded
	}
	respond(w, 200, map[string]any{"reviews": rows, "mine": mine, "nextCursor": next})
}
func (a *API) putReview(w http.ResponseWriter, r *http.Request) {
	widget, ok := a.widget(w, r)
	if !ok {
		return
	}
	if !onlyQuery(r) {
		fail(w, 400, "invalid_query", "No query parameters are accepted.")
		return
	}
	hash, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Rating int     `json:"rating"`
		Body   *string `json:"body"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if input.Rating < 1 || input.Rating > 5 || input.Body == nil || utf8.RuneCountInString(*input.Body) > 2000 || hasDisallowedControl(*input.Body) {
		fail(w, 400, "invalid_review", "Rating must be 1 to 5 and body at most 2000 Unicode characters.")
		return
	}
	review, err := a.Store.PutReview(r.Context(), widget, hash, input.Rating, strings.TrimSpace(*input.Body))
	if err != nil {
		unavailable(w)
		return
	}
	summary, err := a.Store.Summaries(r.Context(), []string{widget})
	if err != nil || len(summary) != 1 {
		unavailable(w)
		return
	}
	respond(w, 200, map[string]any{"review": review, "summary": summary[0]})
}
func (a *API) deleteReview(w http.ResponseWriter, r *http.Request) {
	widget, ok := a.widget(w, r)
	if !ok {
		return
	}
	if !onlyQuery(r) {
		fail(w, 400, "invalid_query", "No query parameters are accepted.")
		return
	}
	hash, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16))
	if err != nil || len(bytes.TrimSpace(body)) > 0 {
		fail(w, 400, "invalid_request", "Delete requires an empty body.")
		return
	}
	if a.Store.DeleteReview(r.Context(), widget, hash) != nil {
		unavailable(w)
		return
	}
	summary, err := a.Store.Summaries(r.Context(), []string{widget})
	if err != nil || len(summary) != 1 {
		unavailable(w)
		return
	}
	respond(w, 200, map[string]any{"deleted": true, "summary": summary[0]})
}
func (a *API) download(w http.ResponseWriter, r *http.Request) {
	widget, ok := a.widget(w, r)
	if !ok {
		return
	}
	if !onlyQuery(r) {
		fail(w, 400, "invalid_query", "No query parameters are accepted.")
		return
	}
	hash, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Version string `json:"version"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !versionPattern.MatchString(input.Version) || len(input.Version) > 64 {
		fail(w, 400, "unknown_version", "Version is not in the signed catalog.")
		return
	}
	known, err := a.Store.HasCatalogVersion(r.Context(), widget, input.Version)
	if err != nil {
		unavailable(w)
		return
	}
	if !known {
		fail(w, 400, "unknown_version", "Version has not appeared in a verified signed catalog.")
		return
	}
	counted, count, err := a.Store.Download(r.Context(), widget, hash, input.Version)
	if err != nil {
		unavailable(w)
		return
	}
	respond(w, 200, map[string]any{"counted": counted, "downloads": count})
}
func hasDisallowedControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return true
		}
	}
	return false
}
func decodeStrict(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return errors.New("invalid UTF-8")
	}
	// All API payloads/cursors are flat objects. Reject duplicate fields rather
	// than letting the JSON decoder silently pick the last value.
	keys := json.NewDecoder(bytes.NewReader(raw))
	opening, err := keys.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("object required")
	}
	seen := make(map[string]bool)
	for keys.More() {
		token, err := keys.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return errors.New("duplicate field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := keys.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := keys.Token(); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func readJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		fail(w, 415, "content_type", "Content-Type must be application/json.")
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err != nil {
		fail(w, 413, "body_too_large", "Request body is too large.")
		return false
	}
	if decodeStrict(raw, target) != nil {
		fail(w, 400, "invalid_json", "Request JSON is invalid.")
		return false
	}
	return true
}
