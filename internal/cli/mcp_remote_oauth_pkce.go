package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

func newMCPRemotePKCE() (string, string, error) {
	verifier, err := mcpRemoteRandomURLToken(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func mcpRemoteRandomURLToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
