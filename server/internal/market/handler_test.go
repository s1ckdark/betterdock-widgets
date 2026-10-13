package market

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Any unhandled store method would panic; validation tests prove no accidental DB writes.
type unavailableStore struct{ Store }

func (unavailableStore) HasInstallation(context.Context, []byte) (bool, error) {
	return false, errors.New("offline")
}
func (unavailableStore) Summaries(context.Context, []string) ([]Summary, error) {
	return nil, errors.New("offline")
}
func (unavailableStore) Ping(context.Context) error { return errors.New("offline") }
func testCatalog() Catalog {
	return Catalog{Versions: map[string]string{"com.betterdock.clock": "1.0.0", "example-uptime": "1.0.0"}, Sequence: 2, ExpiresAt: time.Now().Add(time.Hour)}
}
func request(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.1:1234"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func TestValidationAndUnavailableNeverBecomeZero(t *testing.T) {
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"GET", "/v1/widgets?ids=com.betterdock.clock", "", "", 503},
		{"GET", "/v1/widgets?ids=unknown", "", "", 404},
		{"GET", "/v1/widgets?ids=com.betterdock.clock&ids=example-uptime", "", "", 400},
		{"GET", "/v1/widgets?ids=com.betterdock.clock;bad", "", "", 400},
		{"GET", "/v1/widgets/com.betterdock.clock/reviews?limit=0", "", "", 400},
		{"GET", "/v1/widgets/com.betterdock.clock/reviews?cursor=bad", "", "", 400},
		{"PUT", "/v1/widgets/com.betterdock.clock/review", "", `{"rating":5,"body":"hi"}`, 401},
		{"DELETE", "/v1/widgets/com.betterdock.clock/review", "bad", "", 401},
		{"POST", "/v1/installations", "", `{}`, 400},
		{"POST", "/v1/widgets/unknown/downloads", "", `{"version":"1.0.0"}`, 404},
		{"POST", "/v1/widgets", "", "", 405},
		{"GET", "/healthz", "", "", 503},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			h := NewAPI(unavailableStore{}, testCatalog(), nil).Handler()
			w := request(h, tc.method, tc.path, tc.token, tc.body)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s want %d", w.Code, w.Body, tc.status)
			}
			if strings.Contains(w.Body.String(), `"downloads":0`) {
				t.Fatal("error became zero")
			}
		})
	}
}
func TestRateLimitsAndAuthDoNotLeak(t *testing.T) {
	api := NewAPI(unavailableStore{}, testCatalog(), nil)
	h := api.Handler()
	for i := 0; i < 5; i++ {
		w := request(h, "POST", "/v1/installations", "", "{}")
		if w.Code != 400 {
			t.Fatalf("unexpected registration status %d", w.Code)
		}
	}
	w := request(h, "POST", "/v1/installations", "", "{}")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("registration limit missing")
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	w = request(h, "GET", "/v1/widgets?ids=com.betterdock.clock", token, "")
	if w.Code != 503 || strings.Contains(w.Body.String(), token) {
		t.Fatal("DB failure exposed token or lost status")
	}
}
func TestReadJSONBounds(t *testing.T) {
	for _, tc := range []struct {
		body, content string
		status        int
	}{
		{`{"unknown":1}`, "application/json", 400}, {`{} {}`, "application/json", 400},
		{`{"body":"one","body":"two"}`, "application/json", 400},
		{"{}", "text/plain", 415}, {strings.Repeat(" ", 32769), "application/json", 413},
		{string([]byte{'{', '"', 'a', '"', ':', '"', 0xff, '"', '}'}), "application/json", 400},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("PUT", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.content)
		var input struct {
			Body string `json:"body"`
		}
		if readJSON(w, r, &input) || w.Code != tc.status {
			t.Fatalf("invalid input accepted status=%d want=%d", w.Code, tc.status)
		}
	}
}
