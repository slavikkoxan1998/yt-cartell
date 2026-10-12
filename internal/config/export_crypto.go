package config

// Encryption of a settings file (export.go): PBKDF2-HMAC-SHA256 stretches the
// password, HKDF-SHA256 splits the result into the AES-256-GCM key and a
// 16-byte password check, and GCM authenticates the header as additional
// data. Everything is the standard library (crypto/pbkdf2, crypto/hkdf).
//
//	master = PBKDF2-HMAC-SHA256(NFC(password), salt[32], iterations, 32)
//	key    = HKDF-Expand(SHA-256, master, "ytcs v1 aes-256-gcm", 32)
//	check  = HKDF-Expand(SHA-256, master, "ytcs v1 password check", 16)
//	data   = AES-256-GCM(key, nonce[12], payload, additional data = canonical header JSON)
//
// The check lets an import tell a wrong password from a damaged file. It
// does not make guessing cheaper: every guess still costs the full PBKDF2
// run, after which GCM would answer the same question.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"

	"golang.org/x/text/unicode/norm"
)

const (
	kdfName    = "pbkdf2-sha256"
	cipherName = "aes-256-gcm"
	// KDFIterations is the PBKDF2 work factor of new files (OWASP's
	// recommendation for PBKDF2-HMAC-SHA256).
	KDFIterations = 600_000
	// An import accepts this range: fewer would be a weak or forged file,
	// more would freeze the studio on a hostile one.
	minKDFIterations = 100_000
	maxKDFIterations = 10_000_000

	saltSize  = 32
	nonceSize = 12
	checkSize = 16

	infoKey   = "ytcs v1 aes-256-gcm"
	infoCheck = "ytcs v1 password check"
)

// deriveKeys stretches the password into the AES key and the password check.
// The password is NFC-normalised, so "й" typed on any system is the same key.
func deriveKeys(password string, salt []byte, iterations int) (key, check []byte, err error) {
	master, err := pbkdf2.Key(sha256.New, norm.NFC.String(password), salt, iterations, 32)
	if err != nil {
		return nil, nil, err
	}
	if key, err = hkdf.Expand(sha256.New, master, infoKey, 32); err != nil {
		return nil, nil, err
	}
	if check, err = hkdf.Expand(sha256.New, master, infoCheck, checkSize); err != nil {
		return nil, nil, err
	}
	return key, check, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// randomBytes reads n bytes from the system's secure random source.
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("немає випадкових чисел для шифрування: %w", err)
	}
	return b, nil
}

// sealedParts are the crypto parameters stored in the header.
type sealedParts struct {
	salt, nonce, check, key []byte
}

// newSeal draws a fresh salt and nonce and derives the keys for password.
func newSeal(password string, iterations int) (sealedParts, error) {
	var s sealedParts
	var err error
	if s.salt, err = randomBytes(saltSize); err != nil {
		return s, err
	}
	if s.nonce, err = randomBytes(nonceSize); err != nil {
		return s, err
	}
	s.key, s.check, err = deriveKeys(password, s.salt, iterations)
	return s, err
}

// seal encrypts plain with the header bytes as additional data.
func (s sealedParts) seal(plain, aad []byte) ([]byte, error) {
	g, err := newGCM(s.key)
	if err != nil {
		return nil, err
	}
	return g.Seal(nil, s.nonce, plain, aad), nil
}

// openSealed checks the password and decrypts. ErrWrongPassword when the
// check does not match; ErrDamaged when it matches but GCM refuses (the data
// or the header was changed).
func openSealed(password string, h ExportHeader, data, aad []byte) ([]byte, error) {
	key, check, err := deriveKeys(password, h.KDF.Salt, h.KDF.Iterations)
	if err != nil {
		return nil, ErrDamaged
	}
	if subtle.ConstantTimeCompare(check, h.Check) != 1 {
		return nil, ErrWrongPassword
	}
	g, err := newGCM(key)
	if err != nil {
		return nil, ErrDamaged
	}
	plain, err := g.Open(nil, h.Cipher.Nonce, data, aad)
	if err != nil {
		return nil, ErrDamaged
	}
	return plain, nil
}
