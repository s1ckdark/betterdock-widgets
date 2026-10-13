package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tlsFixture(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "racknerd"}, DNSNames: []string{"racknerd"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("fixture certificate invalid")
	}
	return certPath, keyPath, roots
}
func TestTLSConfigurationFailsClosed(t *testing.T) {
	if config, err := loadTLSConfig("", ""); err != nil || config != nil {
		t.Fatal("HTTP mode must stay available without TLS files")
	}
	for _, paths := range [][2]string{{"cert", ""}, {"", "key"}, {"missing-cert", "missing-key"}} {
		if _, err := loadTLSConfig(paths[0], paths[1]); err == nil {
			t.Fatal("invalid TLS config fell back to plaintext")
		}
	}
	cert, key, _ := tlsFixture(t)
	config, err := loadTLSConfig(cert, key)
	if err != nil || config.MinVersion != tls.VersionTLS13 || len(config.Certificates) != 1 {
		t.Fatalf("TLS configuration invalid: %v", err)
	}
	otherCert, _, _ := tlsFixture(t)
	if _, err := loadTLSConfig(otherCert, key); err == nil {
		t.Fatal("mismatched certificate/key accepted")
	}
}
func TestNativeTLSHandshakeAndMinimumVersion(t *testing.T) {
	cert, key, roots := tlsFixture(t)
	config, err := loadTLSConfig(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = config
	server.StartTLS()
	defer server.Close()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13, ServerName: "racknerd"}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 204 || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
		t.Fatal("TLS 1.3 handshake did not succeed")
	}
	oldTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, ServerName: "racknerd"}}
	defer oldTransport.CloseIdleConnections()
	if response, err := (&http.Client{Transport: oldTransport, Timeout: time.Second}).Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("TLS 1.2 was accepted")
	}
	wrongHostTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13, ServerName: "wrong-host"}}
	defer wrongHostTransport.CloseIdleConnections()
	if response, err := (&http.Client{Transport: wrongHostTransport, Timeout: time.Second}).Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("wrong hostname was accepted")
	}
}
