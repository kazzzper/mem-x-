package cli_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"mem-x/internal/cli"
)

// selfSignedCert returns a TLS certificate valid for localhost with a nil
// leaf (used only to prove TLS works in tests).
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestDialTLS(t *testing.T) {
	cert := selfSignedCert(t)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Reply +PONG to a PING.
		if _, err := conn.Write([]byte("+PONG\r\n")); err != nil {
			return
		}
	}()

	c, err := cli.DialTLS(ln.Addr().String(), 2*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	reply, _, err := c.Do([]byte("PING"))
	if err != nil {
		t.Fatal(err)
	}
	if string(reply.Str) != "PONG" {
		t.Fatalf("got %q, want PONG", reply.Str)
	}
	<-done
}

func TestDialTLSSkipVerifyFalseFails(t *testing.T) {
	cert := selfSignedCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// The self-signed cert is not in the system trust store, so verification
	// must fail when skipVerify is false.
	_, err = cli.DialTLS(ln.Addr().String(), time.Second, false)
	if err == nil {
		t.Fatal("expected TLS verification failure with skipVerify=false")
	}
}

// Ensure the client can also do a full round trip through a bufio-wrapped
// TLS connection (the server path used by cmd/mem-x with TLS enabled).
func TestDialTLSAcceptLoop(t *testing.T) {
	cert := selfSignedCert(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
		go func() {
			<-ctx.Done()
			_ = tlsLn.Close()
		}()
		conn, err := tlsLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// TLS handshake completes during Accept; answer the PING without
		// parsing the request (the client is only checking the round trip).
		conn.Write([]byte("+PONG\r\n"))
	}()

	c, err := cli.DialTLS(ln.Addr().String(), 2*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	reply, _, err := c.Do([]byte("PING"))
	if err != nil {
		t.Fatal(err)
	}
	if string(reply.Str) != "PONG" {
		t.Fatalf("got %q, want PONG", reply.Str)
	}
	cancel()
	<-done
}
