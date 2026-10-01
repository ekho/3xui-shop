package testkit

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// SMTP runs a real TLS SMTP conversation on an isolated local socket.
type SMTP struct {
	Address string
	Roots   *x509.CertPool
	mu      sync.Mutex
	letters []string
}

func MailServer(t *testing.T) *SMTP {
	t.Helper()
	https := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificates := https.TLS.Certificates
	roots := x509.NewCertPool()
	roots.AddCert(https.Certificate())
	https.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: certificates, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	server := &SMTP{Address: listener.Addr().String(), Roots: roots}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server
}
func (s *SMTP) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	fmt.Fprint(conn, "220 localhost ESMTP\r\n")
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			fmt.Fprint(conn, "250 localhost\r\n")
		case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
			fmt.Fprint(conn, "250 OK\r\n")
		case line == "DATA":
			fmt.Fprint(conn, "354 Send data\r\n")
			var body strings.Builder
			for scanner.Scan() {
				if scanner.Text() == "." {
					break
				}
				body.WriteString(scanner.Text())
				body.WriteByte('\n')
			}
			s.mu.Lock()
			s.letters = append(s.letters, body.String())
			s.mu.Unlock()
			fmt.Fprint(conn, "250 Queued\r\n")
		case line == "QUIT":
			fmt.Fprint(conn, "221 Bye\r\n")
			return
		default:
			fmt.Fprint(conn, "500 Unsupported\r\n")
		}
	}
}
func (s *SMTP) Letters() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.letters...)
}
