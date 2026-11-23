package vaultexample

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"html/template"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const viewerAddress = "127.0.0.1:39480"
const webhookAddress = "127.0.0.1:39481"

//go:embed viewer.html
var viewerHTML string
var viewerPage = template.Must(template.New("viewer").Parse(viewerHTML))

type delivery struct{ Time, Source, Scheme, Path string }
type webhookServer struct {
	page, api *http.Server
	client    *http.Client
	cert      string
	origin    string
	mu        sync.Mutex
	history   []delivery
}

// The trust root is unique to this process. Only its public certificate crosses the SDK connection.

func loopbackCertificate() (tls.Certificate, *x509.CertPool, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, "", err
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "SDM Vault Example"},
		NotBefore:    now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0),
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, "", err
	}
	public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(public)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots, string(public), nil
}

func startWebhookServer(apiAddress, pageAddress string) (*webhookServer, error) {
	identity, roots, public, err := loopbackCertificate()
	if err != nil {
		return nil, err
	}
	api, err := net.Listen("tcp4", apiAddress)
	if err != nil {
		return nil, err
	}
	page, err := net.Listen("tcp4", pageAddress)
	if err != nil {
		api.Close()
		return nil, err
	}
	s := &webhookServer{
		cert: public, origin: "https://" + api.Addr().String(),
		client: &http.Client{
			Timeout:       4 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
		},
	}
	serve := func(listener net.Listener, handler http.HandlerFunc) *http.Server {
		host := listener.Addr().String()
		srv := &http.Server{Addr: host, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if r.Host != host || r.URL.RawQuery != "" {
				http.Error(w, "Invalid destination", 400)
				return
			}
			handler(w, r)
		}), ReadTimeout: 4 * time.Second, WriteTimeout: 4 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
		go srv.Serve(listener)
		return srv
	}
	s.api = serve(tls.NewListener(api, &tls.Config{Certificates: []tls.Certificate{identity}, MinVersion: tls.VersionTLS12}), s.receive)
	s.page = serve(page, s.view)
	return s, nil
}

func (s *webhookServer) close() {
	s.api.Close()
	s.page.Close()
	s.client.CloseIdleConnections()
}

func (s *webhookServer) view(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/webhooks/") {
		s.receive(w, r)
		return
	}
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		return
	}
	switch r.URL.Path {
	case "/certificate.pem":
		w.Header().Set("Content-Type", "application/x-pem-file")
		io.WriteString(w, s.cert)
	case "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		s.mu.Lock()
		rows := append([]delivery(nil), s.history...)
		s.mu.Unlock()
		viewerPage.Execute(w, rows)
	default:
		http.NotFound(w, r)
	}
}

func (s *webhookServer) receive(w http.ResponseWriter, r *http.Request) {
	source := map[string]string{"/webhooks/plugin": "Plugin", "/webhooks/manager": "Manager", "/webhooks/example": "Manual"}[r.URL.Path]
	if source == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(415)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128))
	if err != nil {
		w.WriteHeader(413)
		return
	}
	var event map[string]string
	if json.Unmarshal(body, &event) != nil || len(event) != 1 || event["event"] != "ping" {
		http.Error(w, "Expected an example ping", 400)
		return
	}
	scheme := "HTTP"
	if r.TLS != nil {
		scheme = "HTTPS"
	}
	s.mu.Lock()
	s.history = append([]delivery{{time.Now().UTC().Format(time.RFC3339), source, scheme, r.URL.Path}}, s.history...)
	if len(s.history) > 64 {
		s.history = s.history[:64]
	}
	s.mu.Unlock()
	w.WriteHeader(204)
}

func (s *webhookServer) post(ctx context.Context, transport string) error {
	origin := s.origin
	if transport == "http" {
		origin = "http://" + s.page.Addr
	}
	req, err := http.NewRequestWithContext(ctx, "POST", origin+"/webhooks/plugin", strings.NewReader(`{"event":"ping"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("local webhook unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return errors.New("local webhook rejected ping")
	}
	return nil
}
