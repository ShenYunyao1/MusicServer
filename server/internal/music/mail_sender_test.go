package music

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testSMTPCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	parsed, _ := x509.ParseCertificate(der)
	roots.AddCert(parsed)
	return cert, roots
}
func startTestSMTP(t *testing.T, mode string) (string, *x509.CertPool, <-chan string) {
	t.Helper()
	cert, roots := testSMTPCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 4)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				if mode == "tls" {
					conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				}
				reader := bufio.NewReader(conn)
				_, _ = io.WriteString(conn, "220 local SMTP\r\n")
				secured := mode == "tls"
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					command := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(command, "EHLO"):
						if mode == "starttls" && !secured {
							_, _ = io.WriteString(conn, "250-local\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n")
						} else {
							_, _ = io.WriteString(conn, "250-local\r\n250 AUTH PLAIN\r\n")
						}
					case command == "STARTTLS":
						_, _ = io.WriteString(conn, "220 begin TLS\r\n")
						conn = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
						reader = bufio.NewReader(conn)
						secured = true
					case strings.HasPrefix(command, "AUTH "):
						if !secured {
							_, _ = io.WriteString(conn, "530 TLS required\r\n")
						} else {
							_, _ = io.WriteString(conn, "235 authenticated\r\n")
						}
					case strings.HasPrefix(command, "MAIL FROM:") || strings.HasPrefix(command, "RCPT TO:"):
						_, _ = io.WriteString(conn, "250 accepted\r\n")
					case command == "DATA":
						_, _ = io.WriteString(conn, "354 send data\r\n")
						var body strings.Builder
						for {
							data, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							if data == ".\r\n" {
								break
							}
							body.WriteString(data)
						}
						messages <- body.String()
						_, _ = io.WriteString(conn, "250 submitted\r\n")
					case command == "QUIT":
						_, _ = io.WriteString(conn, "221 bye\r\n")
						return
					default:
						_, _ = io.WriteString(conn, "500 unsupported\r\n")
					}
				}
			}(connection)
		}
	}()
	return listener.Addr().String(), roots, messages
}
func TestMailSMTPVerifiedTLSAndNoDowngrade(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			address, roots, messages := startTestSMTP(t, mode)
			host, portText, _ := net.SplitHostPort(address)
			port, _ := strconv.Atoi(portText)
			settings := MailSettings{Host: host, Port: port, TLSMode: mode, FromAddress: "sender@example.com", Username: "sender"}
			envelope := mailEnvelope{To: "target@example.com", Code: "123456"}
			if err := sendSMTP(context.Background(), settings, mailSecret{Password: "password"}, envelope, "idempotent-message", nil); err == nil {
				t.Fatal("untrusted certificate accepted")
			}
			if err := sendSMTP(context.Background(), settings, mailSecret{Password: "password"}, envelope, "idempotent-message", roots); err != nil {
				t.Fatal(err)
			}
			select {
			case body := <-messages:
				if !strings.Contains(body, "123456") || !strings.Contains(body, "Message-ID: <idempotent-message@example.com>") {
					t.Fatal(body)
				}
			case <-time.After(time.Second):
				t.Fatal("message missing")
			}
		})
	}
	address, roots, _ := startTestSMTP(t, "plain")
	host, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	if err := sendSMTP(context.Background(), MailSettings{Host: host, Port: port, TLSMode: "starttls", FromAddress: "sender@example.com", Username: "sender"}, mailSecret{Password: "password"}, mailEnvelope{To: "to@example.com", Test: true}, "test", roots); err == nil || err.Error() != "SMTP_TLS_REQUIRED" {
		t.Fatal("SMTP downgraded", err)
	}
}

func TestMailSMTPCancelInterruptsHandshake(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				<-ctx.Done()
			}()
			host, portText, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(portText)
			started := time.Now()
			time.AfterFunc(30*time.Millisecond, cancel)
			err = sendSMTP(ctx, MailSettings{Host: host, Port: port, TLSMode: mode, FromAddress: "sender@example.com", Username: "sender"}, mailSecret{Password: "password"}, mailEnvelope{To: "to@example.com"}, "cancel", nil)
			if err == nil || time.Since(started) > time.Second {
				t.Fatal("cancellation did not interrupt handshake", err, time.Since(started))
			}
		})
	}
}
