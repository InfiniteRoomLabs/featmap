package main

import (
	"crypto/rand"
	"encoding/base64"
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
