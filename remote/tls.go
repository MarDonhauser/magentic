package remote

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// LoadOrCreateCertificate lädt das selbstsignierte Host-Zertifikat oder
// erzeugt es beim ersten Start. Schlüssel und Zertifikat ruhen owner-only im
// Host-Verzeichnis; ausgestellt wird auf „magentic-host", gegolten wird ein
// Jahrzehnt — die Bindung leistet das Pinning, nicht die CA-Kette.
func LoadOrCreateCertificate(dir string) (tls.Certificate, error) {
	certPath := filepath.Join(dir, "host-cert.pem")
	keyPath := filepath.Join(dir, "host-key.pem")
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return tls.LoadX509KeyPair(certPath, keyPath)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial := make([]byte, 16)
	if _, err := rand.Read(serial); err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: new(big.Int).SetBytes(serial),
		Subject:      pkix.Name{CommonName: "magentic-host"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"magentic-host", "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

// CertFingerprint pinnt das Zertifikat: SHA-256 über das rohe DER, base64.
// Der Client merkt es sich beim ersten Attach und verweigert danach jede
// Änderung — TOFU statt CA.
func CertFingerprint(cert tls.Certificate) (string, error) {
	if len(cert.Certificate) == 0 {
		return "", fmt.Errorf("Zertifikat ist leer")
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// FingerprintOfDER berechnet denselben Pin aus rohen DER-Bytes (Client-Seite
// aus dem Handshake).
func FingerprintOfDER(der []byte) string {
	sum := sha256.Sum256(der)
	return base64.StdEncoding.EncodeToString(sum[:])
}
