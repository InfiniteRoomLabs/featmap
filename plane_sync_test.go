package main

import (
	"context"
	"testing"
)

func Test_SetGetPlaneConnection_encrypts(t *testing.T) {
	runInTx(t, func(t *testing.T, ctx context.Context, s Service, acc *Account, ws *Workspace, member *Member) {
		// service config must carry an encryption key for this test
		s.SetConfig(Configuration{Environment: "development", Mode: "selfhost", PlaneEncryptionKey: testKeyB64(t)})
		fx := newProjectFixture(t, s)

		conn, err := s.SetPlaneConnection(fx.Project.ID, "https://api.plane.so", "ws-slug", "secret_key_1234", "p1,p2")
		mustOK(t, err, "SetPlaneConnection")
		if conn.APIKeyHint != "1234" {
			t.Fatalf("hint should be last 4, got %q", conn.APIKeyHint)
		}
		if string(conn.APIKeyCipher) == "secret_key_1234" {
			t.Fatal("api key stored in plaintext")
		}

		got, err := s.GetPlaneConnection(fx.Project.ID)
		mustOK(t, err, "GetPlaneConnection")
		if got.PlaneWorkspace != "ws-slug" || got.WatchedProjects != "p1,p2" {
			t.Fatalf("connection round-trip mismatch: %+v", got)
		}
	})
}
