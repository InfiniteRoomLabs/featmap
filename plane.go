package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// Plane integration: REST client, credential crypto, and the value-typed enums
// backing plane_comment_origin / plane_sync_status. The sync engine itself lives
// on the service (service.go); this file is the Plane-facing edge + helpers.

// CommentOrigin mirrors the plane_comment_origin Postgres enum.
type CommentOrigin string

const (
	OriginFeatmap CommentOrigin = "featmap"
	OriginPlane   CommentOrigin = "plane"
)

func (o CommentOrigin) Valid() bool { return o == OriginFeatmap || o == OriginPlane }

// SyncStatus mirrors the plane_sync_status Postgres enum.
type SyncStatus string

const (
	StatusOK      SyncStatus = "ok"
	StatusError   SyncStatus = "error"
	StatusPending SyncStatus = "pending"
)

func (s SyncStatus) Valid() bool {
	return s == StatusOK || s == StatusError || s == StatusPending
}

// decodeKey turns the base64 conf key into a 32-byte AES-256 key.
func decodeKey(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, errors.New("planeEncryptionKey not configured")
	}
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("planeEncryptionKey is not valid base64")
	}
	if len(k) != 32 {
		return nil, errors.New("planeEncryptionKey must decode to 32 bytes (AES-256)")
	}
	return k, nil
}

// encryptPlaneKey AES-256-GCM encrypts a Plane API key. Returns ciphertext +
// the per-record nonce. The encryption key is the base64 conf value.
func encryptPlaneKey(keyB64, plaintext string) (ciphertext, nonce []byte, err error) {
	key, err := decodeKey(keyB64)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

// decryptPlaneKey reverses encryptPlaneKey.
func decryptPlaneKey(keyB64 string, ciphertext, nonce []byte) (string, error) {
	key, err := decodeKey(keyB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("failed to decrypt plane key (wrong key or corrupt data)")
	}
	return string(plaintext), nil
}
