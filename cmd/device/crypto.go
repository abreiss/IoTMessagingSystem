package main

import (
	"crypto/aes"      // AES block cipher
	"crypto/cipher"   // GCM authenticated encryption mode
	"crypto/ecdh"     // X25519 key exchange
	"crypto/rand"     // cryptographic random bytes
	"crypto/sha256"   // SHA-256 hash for HKDF
	"encoding/base64" // base64 encode/decode public keys
	"fmt"             // error formatting
	"io"              // read from HKDF reader

	"golang.org/x/crypto/hkdf" // key derivation from shared secret
)

// generateKeyPair creates an ephemeral X25519 keypair stored in memory only
// key clamping is handled automatically by crypto/ecdh
func generateKeyPair() (*ecdh.PrivateKey, error) {
	curve := ecdh.X25519()
	return curve.GenerateKey(rand.Reader)
}

// deriveSharedKey performs X25519 ECDH with the peer's public key
// then feeds the raw secret through HKDF-SHA256 to produce a 32-byte AES-256 key.
// Both devices run this with swapped keys and arrive at the same result.
func deriveSharedKey(privKey *ecdh.PrivateKey, peerPubKeyBytes []byte) ([]byte, error) {
	// parse peer's raw public key bytes into an ecdh.PublicKey
	curve := ecdh.X25519()
	peerPubKey, err := curve.NewPublicKey(peerPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid peer public key: %w", err)
	}

	// ECDH: ourPriv * theirPub = raw shared secret (same on both sides)
	rawSecret, err := privKey.ECDH(peerPubKey)
	if err != nil {
		return nil, fmt.Errorf("ECDH failed: %w", err)
	}

	// HKDF-SHA256: stretch raw secret into a proper AES-256 key
	// info string binds the key to this application
	info := []byte("iot-messaging-v1")
	hkdfReader := hkdf.New(sha256.New, rawSecret, nil, info)
	aesKey := make([]byte, 32) // 256 bits for AES-256
	_, err = io.ReadFull(hkdfReader, aesKey)
	if err != nil {
		return nil, fmt.Errorf("HKDF key derivation failed: %w", err)
	}

	return aesKey, nil
}

// encryptMessage encrypts plaintext with AES-256-GCM using the shared session key.
// Returns nonce (12 bytes) prepended to ciphertext+tag so the receiver can split them.
func encryptMessage(key, plaintext []byte) ([]byte, error) {
	// build AES cipher block from 32-byte key
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("AES cipher init failed: %w", err)
	}

	// wrap block in GCM mode for authenticated encryption
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("GCM init failed: %w", err)
	}

	// random 12-byte nonce — must be unique per message
	nonce := make([]byte, gcm.NonceSize())
	_, err = rand.Read(nonce)
	if err != nil {
		return nil, fmt.Errorf("nonce generation failed: %w", err)
	}

	// Seal appends ciphertext+tag after the nonce: nonce || ciphertext || tag
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return sealed, nil
}

// decryptMessage decrypts data produced by encryptMessage.
// Splits the leading nonce from the ciphertext+tag, then decrypts and verifies.
func decryptMessage(key, data []byte) ([]byte, error) {
	// build AES cipher block from 32-byte key
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("AES cipher init failed: %w", err)
	}

	// wrap block in GCM mode for authenticated decryption
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("GCM init failed: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("data too short to contain nonce")
	}

	// split nonce from ciphertext+tag
	nonce := data[:nonceSize]
	ciphertext := data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM decryption failed (tampered or wrong key): %w", err)
	}

	return plaintext, nil
}

// encodePubKey base64-encodes a public key for transmission in a handshake envelope
func encodePubKey(pub *ecdh.PublicKey) string {
	pubKeyBytes := pub.Bytes()
	return base64.StdEncoding.EncodeToString(pubKeyBytes)
}

// decodePubKey decodes a base64 string back to raw public key bytes
func decodePubKey(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
