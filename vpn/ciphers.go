package vpn

//
// Code to perform encryption and decryption
//

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
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

	// errPadding indicates that a padding error has occurred.
	errPadding = errors.New("padding error")

	// errUnsupportedCipher indicates we don't support the desired cipher.
	errUnsupportedCipher = errors.New("unsupported cipher")

	// errUnsupportedMode indicates that the mode is not uspported.
	errUnsupportedMode = errors.New("unsupported mode")
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

// decrypt implements dataCipher.decrypt
func (a *dataCipherAES) decrypt(key, iv, ciphertext, ad []byte) ([]byte, error) {
	if len(key) != a.keySizeBytes() {
		return nil, errInvalidKeySize
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	switch a.mode {
	case cipherModeCBC:
		i := iv[:block.BlockSize()]
		mode := cipher.NewCBCDecrypter(block, i)
		plaintext := make([]byte, len(ciphertext))
		mode.CryptBlocks(plaintext, ciphertext)
		plaintext = cipherUnpadTextPKCS7(plaintext)
		padLen := len(ciphertext) - len(plaintext)
		if padLen > block.BlockSize() || padLen > len(plaintext) {
			// TODO(bassosimone, ainghazal): discuss the cases in which
			// this set of conditions actually occurs.
			return nil, errPadding
		}
		return plaintext, nil

	case cipherModeGCM:
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

// encrypt implements dataCipher.encrypt
func (a *dataCipherAES) encrypt(key, iv, plaintext, ad []byte) ([]byte, error) {
	if len(key) != a.keySizeBytes() {
		return nil, errInvalidKeySize
	}

	block, err := aes.NewCipher(key)
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

// TODO(bassosimone, ainghazal): we should make the two following
// functions more robust to errors.

// cipherUnpadTextPKCS7 does PKCS#7 unpadding of a byte array.
func cipherUnpadTextPKCS7(buf []byte) []byte {
	// TODO(bassosimone, ainghazal): explain how this function will
	// behave in case there's no padding? Should we pass to the function
	// the expected block size and use that to determine whether there
	// is any padding to be removed from the function?
	padding := int(buf[len(buf)-1])
	return buf[:len(buf)-padding]
}

// cipherPadTextPKCS7 does PKCS#7 padding of a byte array.
func cipherPadTextPKCS7(buf []byte, bs int) []byte {
	// TODO(bassosimone, ainghazal): what happens if we need to add
	// no padding? Should we have a special case to handle that in
	// order to make the code more clear and explicit.
	padding := bs - len(buf)%bs
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(buf, padtext...)
}
