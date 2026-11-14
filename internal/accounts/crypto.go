package accounts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

var errKey = errors.New("accounts: token key must be 32 bytes")

// seal encrypts v with AES-256-GCM under key; the random nonce is
// prepended to the ciphertext. aad binds the ciphertext to its row
// ("<user_id>|<provider>|<field>"), so copying it elsewhere fails to open. An empty value stays NULL-like (nil).
func seal(key []byte, v, aad string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, []byte(v), []byte(aad)), nil
}

// open reverses seal. A wrong key or damaged data is an error.
func open(key, blob []byte, aad string) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	g, err := gcm(key)
	if err != nil {
		return "", err
	}
	if len(blob) < g.NonceSize() {
		return "", errors.New("accounts: stored token is too short")
	}
	n := g.NonceSize()
	p, err := g.Open(nil, blob[:n], blob[n:], []byte(aad))
	if err != nil {
		return "", errors.New("accounts: cannot decrypt stored token (wrong key?)")
	}
	return string(p), nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errKey
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
