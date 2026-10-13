package market

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const PublicKeyBase64 = "pwO6l6HlEPMEaOxjIrBNZsjC5yPWHJvhQFsfuiZ+DXU="

var widgetIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Catalog struct {
	Versions  map[string]string
	ExpiresAt time.Time
	Sequence  int64
}

func LoadCatalog(path, signaturePath string) (Catalog, error) {
	data, err := readLimitedFile(path, 8<<20)
	if err != nil {
		return Catalog{}, err
	}
	sig, err := readLimitedFile(signaturePath, 1024)
	if err != nil {
		return Catalog{}, err
	}
	key, _ := base64.StdEncoding.DecodeString(PublicKeyBase64)
	return verifyCatalog(data, sig, ed25519.PublicKey(key))
}
func readLimitedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("catalog file unavailable")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("catalog file too large or unreadable")
	}
	return b, nil
}
func verifyCatalog(data, signature []byte, key ed25519.PublicKey) (Catalog, error) {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, data, sig) {
		return Catalog{}, errors.New("catalog signature invalid")
	}
	var index struct {
		SchemaVersion int       `json:"schemaVersion"`
		Sequence      int64     `json:"sequence"`
		GeneratedAt   time.Time `json:"generatedAt"`
		ExpiresAt     time.Time `json:"expiresAt"`
		Widgets       []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"widgets"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return Catalog{}, errors.New("catalog JSON invalid")
	}
	if index.SchemaVersion != 1 || index.Sequence < 2 || len(index.Widgets) == 0 || len(index.Widgets) > 10000 || index.GeneratedAt.IsZero() || !index.ExpiresAt.After(index.GeneratedAt) {
		return Catalog{}, errors.New("catalog metadata invalid")
	}
	c := Catalog{Versions: map[string]string{}, ExpiresAt: index.ExpiresAt, Sequence: index.Sequence}
	for _, w := range index.Widgets {
		if !widgetIDPattern.MatchString(w.ID) || !versionPattern.MatchString(w.Version) || len(w.Version) > 64 {
			return Catalog{}, errors.New("catalog widget invalid")
		}
		if _, exists := c.Versions[w.ID]; exists {
			return Catalog{}, errors.New("catalog duplicate widget")
		}
		c.Versions[w.ID] = w.Version
	}
	// Expiry does not erase historical reviews/downloads. Downloads still accept
	// only the exact signed version; app installation independently enforces expiry.
	return c, nil
}
