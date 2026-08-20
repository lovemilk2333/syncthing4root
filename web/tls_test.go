package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadAndroidCertDirs: a cacerts-style directory of PEM files (plus a
// garbage file that must be skipped) must end up usable as a trust store.
func TestLoadAndroidCertDirs(t *testing.T) {
	caKey, caDER := makeCert(t, nil, nil, true)
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, leafDER := makeCert(t, caCert, caKey, false)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test_ca.0"),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "garbage.bin"), []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	loadAndroidCertDirs(pool, dir)

	// serve a cert signed by the CA, verify with the loaded pool
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	server := &http.Server{
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{{
			Certificate: [][]byte{leafDER},
			PrivateKey:  leafKey,
		}}},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	go server.ServeTLS(ln, "", "")

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
	url := fmt.Sprintf("https://localhost:%d/", ln.Addr().(*net.TCPAddr).Port)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("TLS handshake failed with loaded pool: %v", err)
	}
	resp.Body.Close()

	// an empty pool must fail — guards against a false-positive test
	empty := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()},
	}}
	if _, err := empty.Get(url); err == nil {
		t.Fatal("empty pool unexpectedly trusted the cert")
	}
}

// TestSystemCertPool: on this (non-Android) host the system pool is present
// and the Android dirs are simply absent — the pool must still be usable.
// (Trust-store content is exercised by TestLoadAndroidCertDirs, which does a
// real TLS handshake; SystemCertPool's subject list is not inspectable.)
func TestSystemCertPool(t *testing.T) {
	pool := systemCertPool()
	if pool == nil {
		t.Fatal("systemCertPool returned nil")
	}
}

// makeCert creates a self-signed CA (parent==nil) or a leaf signed by
// parent with parentKey.
func makeCert(t *testing.T, parent *x509.Certificate, parentKey *rsa.PrivateKey, isCA bool) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "syncthing4root test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if !isCA {
		tmpl.DNSNames = []string{"localhost"}
	}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}
