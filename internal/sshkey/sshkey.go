// Package sshkey makes and reads the lab's SSH key: an Ed25519 key in
// OpenSSH's own private key format, which ssh reads without a passphrase,
// and its public key as a line of authorized_keys. Go's standard library
// has no SSH; the formats are small enough to write here.
package sshkey

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
)

const (
	keyType   = "ssh-ed25519"
	pemType   = "OPENSSH PRIVATE KEY"
	magic     = "openssh-key-v1\x00"
	blockSize = 8 // of the cipher "none"
)

// New returns a new key: the private key as OpenSSH writes it, and the
// public key as a line of authorized_keys with the comment.
func New(comment string) (private []byte, public string, err error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	publicBlob := wireString(nil, []byte(keyType))
	publicBlob = wireString(publicBlob, publicKey)
	check := make([]byte, 4)
	if _, err := rand.Read(check); err != nil {
		return nil, "", err
	}
	var secret []byte
	secret = append(secret, check...)
	secret = append(secret, check...) // twice: ssh compares them after decrypting
	secret = wireString(secret, []byte(keyType))
	secret = wireString(secret, publicKey)
	secret = wireString(secret, privateKey) // the seed and the public key
	secret = wireString(secret, []byte(comment))
	for i := byte(1); len(secret)%blockSize != 0; i++ {
		secret = append(secret, i)
	}
	data := []byte(magic)
	data = wireString(data, []byte("none")) // cipher
	data = wireString(data, []byte("none")) // key derivation
	data = wireString(data, nil)            // its options
	data = binary.BigEndian.AppendUint32(data, 1)
	data = wireString(data, publicBlob)
	data = wireString(data, secret)
	private = pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: data})
	return private, authorizedLine(publicBlob, comment), nil
}

// Public returns the public key of a private key that New made, as a line
// of authorized_keys with the comment.
func Public(private []byte, comment string) (string, error) {
	block, _ := pem.Decode(private)
	if block == nil || block.Type != pemType {
		return "", errors.New("not an OpenSSH private key")
	}
	rest, found := bytes.CutPrefix(block.Bytes, []byte(magic))
	if !found {
		return "", errors.New("not an OpenSSH private key")
	}
	for range 3 { // cipher, key derivation, its options
		if _, rest, found = readString(rest); !found {
			return "", errors.New("the OpenSSH private key is cut short")
		}
	}
	if len(rest) < 4 || binary.BigEndian.Uint32(rest) != 1 {
		return "", errors.New("the OpenSSH private key does not hold one key")
	}
	publicBlob, _, found := readString(rest[4:])
	if !found {
		return "", errors.New("the OpenSSH private key is cut short")
	}
	name, key, found := readString(publicBlob)
	if !found || string(name) != keyType {
		return "", fmt.Errorf("the OpenSSH private key is not of the type %s", keyType)
	}
	if key, _, found := readString(key); !found || len(key) != ed25519.PublicKeySize {
		return "", errors.New("the OpenSSH private key has a broken public key")
	}
	return authorizedLine(publicBlob, comment), nil
}

// authorizedLine returns a public key as a line of authorized_keys.
func authorizedLine(publicBlob []byte, comment string) string {
	return keyType + " " + base64.StdEncoding.EncodeToString(publicBlob) + " " + comment
}

// wireString appends value to data as SSH's wire format has strings: its
// length in four bytes, then the bytes.
func wireString(data, value []byte) []byte {
	data = binary.BigEndian.AppendUint32(data, uint32(len(value)))
	return append(data, value...)
}

// readString reads a string of SSH's wire format from data and returns it
// with the rest; found is false if data is too short.
func readString(data []byte) (value, rest []byte, found bool) {
	if len(data) < 4 {
		return nil, nil, false
	}
	length := binary.BigEndian.Uint32(data)
	if uint64(len(data)-4) < uint64(length) {
		return nil, nil, false
	}
	return data[4 : 4+length], data[4+length:], true
}
