package payment

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
)

// parseRSAPrivateKey parses a PEM-encoded RSA private key.
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		// Try raw PKCS#8
		key, err := x509.ParsePKCS8PrivateKey([]byte(pemStr))
		if err != nil {
			// Try PKCS#1
			key2, err2 := x509.ParsePKCS1PrivateKey([]byte(pemStr))
			if err2 != nil {
				return nil, fmt.Errorf("failed to parse private key: %v / %v", err, err2)
			}
			return key2, nil
		}
		return key.(*rsa.PrivateKey), nil
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		key2, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parse PEM block: %w", err)
		}
		return key2, nil
	}
	return key.(*rsa.PrivateKey), nil
}

// parseRSAPublicKey parses a PEM-encoded RSA public key.
func parseRSAPublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		key, err := x509.ParsePKIXPublicKey([]byte(pemStr))
		if err != nil {
			return nil, err
		}
		return key.(*rsa.PublicKey), nil
	}

	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return key.(*rsa.PublicKey), nil
}

// signRSASHA256 signs a message with RSA-SHA256.
func signRSASHA256(message string, key *rsa.PrivateKey) (string, error) {
	hash := sha256.New()
	hash.Write([]byte(message))
	digest := hash.Sum(nil)

	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

// verifyRSASHA256 verifies an RSA-SHA256 signature.
func verifyRSASHA256(message string, signatureB64 string, key *rsa.PublicKey) error {
	signature, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	hash := sha256.New()
	hash.Write([]byte(message))
	digest := hash.Sum(nil)

	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest, signature)
}
