package vaultexample

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestViewerAcceptsOnlyBoundedPingsAndUsesVerifiedTLS(t *testing.T) {
	s, err := startWebhookServer("127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for _, transport := range []string{"https", "http"} {
		if err = s.post(context.Background(), transport); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = http.Get(s.origin + "/webhooks/plugin"); err == nil {
		t.Fatal("session identity trusted without certificate")
	}
	check := func(method, path, body string, want int) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.receive(w, r)
		if w.Code != want {
			t.Errorf("%s %s: %d", method, path, w.Code)
		}
	}
	check("POST", "/webhooks/example", `{"event":"ping","value":"private"}`, 400)
	check("GET", "/webhooks/example", "", 405)
	check("POST", "/webhooks/unknown", "", 404)
	check("POST", "/webhooks/example", strings.Repeat("x", 200), 413)
	for i := 0; i < 70; i++ {
		check("POST", "/webhooks/example", `{"event":"ping"}`, 204)
	}
	res, err := s.client.Get("http://" + s.page.Addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	page, _ := io.ReadAll(res.Body)
	if strings.Count(string(page), "<td>ping</td>") != 64 || strings.Contains(string(page), "private") {
		t.Error("invalid viewer history")
	}
	r, _ := http.NewRequest("GET", "http://"+s.page.Addr+"/", nil)
	r.Host = "other.example"
	res, err = s.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("unexpected host accepted")
	}
}
