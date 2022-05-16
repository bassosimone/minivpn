package vpn

//
// Code to perform encryption, decryption and key derivation.
//

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"log"
)

// TODO(ainghazal,bassosimone): see if it's feasible to use stdlib
// functionality rather than using the code below.

type (
	// cipherMode describes a cipher mode (e.g., GCM).
	cipherMode string

	// cipherName is a cipher name (e.g., AES).
	cipherName string
)

const (
	// cipherModeCBC is the CBC cipher mode.
	cipherModeCBC = cipherMode("cbc")

	// cipherModeGCM is the GCM cipher mode.
	cipherModeGCM = cipherMode("gcm")

	// cipherNameAES is an AES-based cipher.
	cipherNameAES = cipherName("aes")
)

var (
	// errInvalidKeySize means that the key size is invalid.
	errInvalidKeySize = errors.New("invalid key size")

	// errUnsupportedCipher indicates we don't support the desired cipher.
	errUnsupportedCipher = errors.New("unsupported cipher")

	// errUnsupportedMode indicates that the mode is not uspported.
	errUnsupportedMode = errors.New("unsupported mode")

	// errBadInput indicates invalid inputs to encrypt/decrypt functions.
	errBadInput = errors.New("bad input")
)

// dataCipher encrypts and decrypts OpenVPN data.
type dataCipher interface {
	// keySizeBytes returns the key size (in bytes).
	keySizeBytes() int

	// isAEAD returns whether this cipher has AEAD properties.
	isAEAD() bool

	// blockSize returns the expected block size.
	blockSize() int

	// encrypt encripts a plaintext.
	//
	// Arguments:
	//
	// - key is the key, whose size must be consistent with the cipher;
	//
	// - iv is the initialization vector;
	//
	// - plaintext is the plaintext to encrypt;
	//
	// - ad contains the additional data (optional and only used for AEAD ciphers).
	//
	// Returns the ciphertext on success and an error on failure.
	encrypt(key, iv, plaintext, ad []byte) ([]byte, error)

	// decrypt is the opposite operation of encrypt. It takes in input the
	// ciphertext and returns the plaintext of an error.
	decrypt(key, iv, ciphertext, ad []byte) ([]byte, error)

	// mode returns the cipherMode
	cipherMode() cipherMode
}

// dataCipherAES implements dataCipher for AES.
type dataCipherAES struct {
	// ksb is the key size in bytes
	ksb int

	// mode is the cipher mode
	mode cipherMode
}

var _ dataCipher = &dataCipherAES{} // Ensure we implement dataCipher

// keySizeBytes implements dataCipher.keySizeBytes
func (a *dataCipherAES) keySizeBytes() int {
	return a.ksb
}

// isAEAD implements dataCipher.isAEAD
func (a *dataCipherAES) isAEAD() bool {
	return a.mode != cipherModeCBC
}

// blockSize implements dataCipher.BlockSize
func (a *dataCipherAES) blockSize() int {
	switch a.mode {
	case cipherModeCBC, cipherModeGCM:
		return 16
	default:
		return 0
	}
}

// decrypt implements dataCipher.decrypt.
// Since key comes from a prf derivation, we only take as many bytes as we need to match
// our key size.
func (a *dataCipherAES) decrypt(key, iv, ciphertext, ad []byte) ([]byte, error) {
	if len(key) < a.keySizeBytes() {
		return nil, errInvalidKeySize
	}
	// they key material might be longer
	k := key[:a.keySizeBytes()]
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	switch a.mode {
	case cipherModeCBC:
		if len(iv) != block.BlockSize() {
			return nil, fmt.Errorf("%w: wrong size for iv: %v", errBadInput, len(iv))
		}
		mode := cipher.NewCBCDecrypter(block, iv)
		plaintext := make([]byte, len(ciphertext))
		mode.CryptBlocks(plaintext, ciphertext)
		plaintext, err := bytesUnpadPKCS7(plaintext, block.BlockSize())
		if err != nil {
			return nil, err
		}
		padLen := len(ciphertext) - len(plaintext)
		if padLen > block.BlockSize() || padLen > len(plaintext) {
			// TODO(bassosimone, ainghazal): discuss the cases in which
			// this set of conditions actually occurs.
			// TODO(ainghazal): this assertion might actually be moved into a
			// boundary assertion in the unpad fun.
			return nil, errors.New("unpadding error")
		}
		return plaintext, nil

	case cipherModeGCM:
		// standard nonce size is 12. more is surely ok, but let's stick to it.
		// https://github.com/golang/go/blob/master/src/crypto/aes/aes_gcm.go#L37
		if len(iv) != 12 {
			return nil, fmt.Errorf("%w: wrong size for iv: %v", errBadInput, len(iv))
		}
		aesGCM, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		plaintext, err := aesGCM.Open(nil, iv, ciphertext, ad)
		if err != nil {
			log.Println("gdm decryption failed:", err.Error())
			log.Println("dump begins----")
			log.Printf("%x\n", ciphertext)
			log.Println("len:", len(ciphertext))
			log.Printf("ad: %x\n", ad)
			log.Println("dump ends------")
			return nil, err
		}
		return plaintext, nil

	default:
		return nil, errUnsupportedMode
	}
}

func (a *dataCipherAES) cipherMode() cipherMode {
	return a.mode
}

// encrypt implements dataCipher.encrypt
// Since key comes from a prf derivation, we only take as many bytes as we need to match
// our key size.
func (a *dataCipherAES) encrypt(key, iv, plaintext, ad []byte) ([]byte, error) {
	if len(key) < a.keySizeBytes() {
		return nil, errInvalidKeySize
	}
	k := key[:a.keySizeBytes()]
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	switch a.mode {
	case cipherModeCBC:
		mode := cipher.NewCBCEncrypter(block, iv) // Note: panics if len(block) != len(iv)
		ciphertext := make([]byte, len(plaintext))
		mode.CryptBlocks(ciphertext, plaintext)
		return ciphertext, nil

	case cipherModeGCM:
		aesGCM, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		// In GCM mode, the IV consists of the 32-bit packet counter
		// followed by data from the HMAC key. The HMAC key can be used
		// as IV, since in GCM mode the HMAC key is not used for the
		// HMAC. The packet counter may not roll over within a single
		// TLS session. This results in a unique IV for each packet, as
		// required by GCM.
		ciphertext := aesGCM.Seal(nil, iv, plaintext, ad)
		return ciphertext, nil

	default:
		return nil, errUnsupportedMode
	}
}

// newDataCipherFromCipherSuite constructs a new dataCipher from the cipher suite string.
func newDataCipherFromCipherSuite(c string) (dataCipher, error) {
	switch c {
	case "AES-128-CBC":
		return newDataCipher(cipherNameAES, 128, cipherModeCBC)
	case "AES-192-CBC":
		return newDataCipher(cipherNameAES, 192, cipherModeCBC)
	case "AES-256-CBC":
		return newDataCipher(cipherNameAES, 256, cipherModeCBC)
	case "AES-128-GCM":
		return newDataCipher(cipherNameAES, 128, cipherModeGCM)
	case "AES-256-GCM":
		return newDataCipher(cipherNameAES, 256, cipherModeGCM)
	default:
		return nil, errUnsupportedCipher
	}
}

// newDataCipher constructs a new dataCipher from the given name, bits, and mode.
func newDataCipher(name cipherName, bits int, mode cipherMode) (dataCipher, error) {
	if bits%8 != 0 || bits > 512 || bits < 64 {
		return nil, fmt.Errorf("%w: %d", errInvalidKeySize, bits)
	}
	switch name {
	case cipherNameAES:
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedCipher, name)
	}
	switch mode {
	case cipherModeCBC, cipherModeGCM:
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedMode, mode)
	}
	dcp := &dataCipherAES{
		ksb:  bits / 8,
		mode: mode,
	}
	return dcp, nil
}

// newHMACFactory accepts a label coming from an OpenVPN auth label, and returns two
// values: a function that will return a Hash implementation, and a boolean
// indicating if the operation was successful.
func newHMACFactory(name string) (func() hash.Hash, bool) {
	switch name {
	case "sha1":
		return sha1.New, true
	case "sha256":
		return sha256.New, true
	case "sha512":
		return sha512.New, true
	default:
		return nil, false
	}
}

// prf function is used to derive master and client keys
func prf(secret, label, clientSeed, serverSeed, clientSid, serverSid []byte, olen int) []byte {
	seed := append(clientSeed, serverSeed...)
	if len(clientSid) != 0 {
		seed = append(seed, clientSid...)
	}
	if len(serverSid) != 0 {
		seed = append(seed, serverSid...)
	}
	result := make([]byte, olen)
	return prf10(result, secret, label, seed)
}

// Code below is taken from crypto/tls/prf.go
// Copyright 2009 The Go Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause
// prf10 implements the TLS 1.0 pseudo-random function, as defined in RFC 2246, Section 5.
func prf10(result, secret, label, seed []byte) []byte {
	hashSHA1 := sha1.New
	hashMD5 := md5.New

	labelAndSeed := make([]byte, len(label)+len(seed))
	copy(labelAndSeed, label)
	copy(labelAndSeed[len(label):], seed)

	s1, s2 := splitPreMasterSecret(secret)
	pHash(result, s1, labelAndSeed, hashMD5)
	result2 := make([]byte, len(result))
	pHash(result2, s2, labelAndSeed, hashSHA1)
	for i, b := range result2 {
		result[i] ^= b
	}
	return result
}

// SPDX-License-Identifier: BSD-3-Clause
// Split a premaster secret in two as specified in RFC 4346, Section 5.
func splitPreMasterSecret(secret []byte) (s1, s2 []byte) {
	s1 = secret[0 : (len(secret)+1)/2]
	s2 = secret[len(secret)/2:]
	return

}

// SPDX-License-Identifier: BSD-3-Clause
// pHash implements the P_hash function, as defined in RFC 4346, Section 5.
func pHash(result, secret, seed []byte, hash func() hash.Hash) {
	h := hmac.New(hash, secret)
	h.Write(seed)
	a := h.Sum(nil)
	j := 0
	for j < len(result) {
		h.Reset()
		h.Write(a)
		h.Write(seed)
		b := h.Sum(nil)
		copy(result[j:], b)
		j += len(b)
		h.Reset()
		h.Write(a)
		a = h.Sum(nil)
	}
}

// TODO(ainghazal): this function is not needed if we use Hash.Size()
func getHashLength(s string) int {
	switch s {
	case "sha1":
		return 20
	case "sha256":
		return 32
	case "sha512":
		return 64
	}
	return 0
}
