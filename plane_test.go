package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testKeyB64(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func Test_planeKeyCrypto_roundtrip(t *testing.T) {
	key := testKeyB64(t)
	plaintext := "plane_api_key_abc123"

	cipher, nonce, err := encryptPlaneKey(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(cipher) == plaintext {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := decryptPlaneKey(key, cipher, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, plaintext)
	}
}

func Test_planeKeyCrypto_missingKey(t *testing.T) {
	if _, _, err := encryptPlaneKey("", "x"); err == nil {
		t.Fatal("expected error for empty encryption key")
	}
}

func Test_planeKeyCrypto_wrongKeyFails(t *testing.T) {
	cipher, nonce, err := encryptPlaneKey(testKeyB64(t), "secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := decryptPlaneKey(testKeyB64(t), cipher, nonce); err == nil {
		t.Fatal("expected decrypt failure with wrong key")
	}
}

func Test_PlaneClient_listComments_paginates(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "k" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if page == 0 {
			page++
			_, _ = w.Write([]byte(`{"results":[{"id":"c1","comment_html":"<p>a</p>","updated_at":"2026-05-01T00:00:00Z","actor":"u1"}],"next_cursor":"x","next_page_results":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"c2","comment_html":"<p>b</p>","updated_at":"2026-05-02T00:00:00Z","actor":"u2"}],"next_cursor":"","next_page_results":false}`))
	}))
	defer srv.Close()

	c := &PlaneClient{BaseURL: srv.URL, APIKey: "k", PlaneWorkspace: "ws", HTTP: srv.Client(), AllowPrivate: true}
	comments, err := c.ListComments(context.Background(), "proj", "wi")
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments across pages, got %d", len(comments))
	}
	if comments[0].ID != "c1" || comments[1].ID != "c2" {
		t.Fatalf("unexpected ids: %+v", comments)
	}
}

func Test_PlaneClient_createComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"new1","comment_html":"<p>hi</p>","updated_at":"2026-05-03T00:00:00Z","actor":"me"}`))
	}))
	defer srv.Close()

	c := &PlaneClient{BaseURL: srv.URL, APIKey: "k", PlaneWorkspace: "ws", HTTP: srv.Client(), AllowPrivate: true}
	cm, err := c.CreateComment(context.Background(), "proj", "wi", "<p>hi</p>")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if cm.ID != "new1" {
		t.Fatalf("expected id new1, got %q", cm.ID)
	}
}

func Test_PlaneClient_testConnection_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c := &PlaneClient{BaseURL: srv.URL, APIKey: "bad", HTTP: srv.Client(), AllowPrivate: true}
	if err := c.TestConnection(context.Background()); err == nil {
		t.Fatal("expected 401 error from TestConnection")
	}
}

func Test_validatePlaneBaseURL_SSRF(t *testing.T) {
	// Blocked when allowPrivate=false: loopback, cloud metadata (link-local),
	// RFC1918, unspecified, and non-http schemes.
	blocked := []string{
		"http://127.0.0.1",
		"http://localhost",                         // resolves to loopback
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://10.0.0.5",
		"http://192.168.1.1:8080",
		"http://0.0.0.0",
		"file:///etc/passwd",
		"ftp://example.com",
		"://nohost",
	}
	for _, u := range blocked {
		if err := validatePlaneBaseURL(u, false); err == nil {
			t.Errorf("expected %q to be rejected with allowPrivate=false", u)
		}
	}

	// Public hosts allowed.
	for _, u := range []string{"https://api.plane.so", "https://8.8.8.8"} {
		if err := validatePlaneBaseURL(u, false); err != nil {
			t.Errorf("expected %q to be allowed, got %v", u, err)
		}
	}

	// allowPrivate=true is the operator opt-in: private/loopback permitted.
	for _, u := range []string{"http://127.0.0.1", "http://10.0.0.5"} {
		if err := validatePlaneBaseURL(u, true); err != nil {
			t.Errorf("expected %q allowed with allowPrivate=true, got %v", u, err)
		}
	}
	// ...but scheme is still enforced even with allowPrivate=true.
	if err := validatePlaneBaseURL("file:///etc/passwd", true); err == nil {
		t.Error("expected non-http scheme rejected even with allowPrivate=true")
	}
}
