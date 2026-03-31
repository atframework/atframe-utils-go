// Copyright 2026 atframework
// Licensed under the MIT licenses.

// Package xxtea implements the XXTEA (Corrected Block TEA) encryption algorithm.
//
// This implementation is compatible with the C++ atframe_utils xxtea implementation
// for cross-language interoperability.
//
// Key setup uses big-endian byte decoding (matching C++ XXTEA_GET_UINT32_BE).
// Buffer data uses little-endian byte decoding (matching C++ reinterpret_cast<uint32_t*>
// on little-endian platforms).
//
// See: https://en.wikipedia.org/wiki/XXTEA
package xxtea

import (
	"encoding/binary"
	"errors"
)

const (
	// KeySize is the size of the key in bytes (4 × uint32 = 16 bytes).
	KeySize = 16

	delta = 0x9e3779b9
)

var (
	ErrInvalidKeySize    = errors.New("xxtea: key must be exactly 16 bytes")
	ErrInvalidBufferSize = errors.New("xxtea: buffer length must be a multiple of 4 and at least 8 bytes")
	ErrOutputTooSmall    = errors.New("xxtea: output buffer too small")
)

// Key holds the 4 × uint32 expanded key for XXTEA.
type Key struct {
	data [4]uint32
}

// Setup initializes a Key from a 16-byte slice using big-endian byte order,
// matching the C++ xxtea_setup function.
func Setup(filled []byte) (Key, error) {
	if len(filled) != KeySize {
		return Key{}, ErrInvalidKeySize
	}

	var k Key
	for i := 0; i < 4; i++ {
		k.data[i] = binary.BigEndian.Uint32(filled[i*4 : i*4+4])
	}
	return k, nil
}

// mx computes the XXTEA mixing function.
// Matches C++ macro: ((z>>5 ^ y<<2) + (y>>3 ^ z<<4)) ^ ((sum^y) + (key[(p&3)^e] ^ z))
func mx(y, z, sum uint32, p, e uint32, key *[4]uint32) uint32 {
	return ((z>>5 ^ y<<2) + (y>>3 ^ z<<4)) ^ ((sum ^ y) + (key[(p&3)^e] ^ z))
}

// EncryptInPlace encrypts the buffer in-place.
// The buffer is treated as an array of little-endian uint32 values.
// len(buffer) must be a multiple of 4 and at least 8 bytes.
func EncryptInPlace(key *Key, buffer []byte) error {
	if len(buffer) == 0 {
		return nil
	}
	if len(buffer)&0x03 != 0 || len(buffer) < 8 {
		return ErrInvalidBufferSize
	}

	n := uint32(len(buffer) >> 2)
	v := make([]uint32, n)

	// Decode little-endian uint32 values from buffer
	for i := uint32(0); i < n; i++ {
		v[i] = binary.LittleEndian.Uint32(buffer[i*4 : i*4+4])
	}

	rounds := 6 + 52/n
	var sum uint32
	z := v[n-1]

	for ; rounds > 0; rounds-- {
		sum += delta
		e := (sum >> 2) & 3
		var p uint32
		for p = 0; p < n-1; p++ {
			y := v[p+1]
			v[p] += mx(y, z, sum, p, e, &key.data)
			z = v[p]
		}
		y := v[0]
		v[n-1] += mx(y, z, sum, p, e, &key.data)
		z = v[n-1]
	}

	// Encode back to buffer
	for i := uint32(0); i < n; i++ {
		binary.LittleEndian.PutUint32(buffer[i*4:i*4+4], v[i])
	}

	return nil
}

// DecryptInPlace decrypts the buffer in-place.
// The buffer is treated as an array of little-endian uint32 values.
// len(buffer) must be a multiple of 4 and at least 8 bytes.
func DecryptInPlace(key *Key, buffer []byte) error {
	if len(buffer) == 0 {
		return nil
	}
	if len(buffer)&0x03 != 0 || len(buffer) < 8 {
		return ErrInvalidBufferSize
	}

	n := uint32(len(buffer) >> 2)
	v := make([]uint32, n)

	// Decode little-endian uint32 values from buffer
	for i := uint32(0); i < n; i++ {
		v[i] = binary.LittleEndian.Uint32(buffer[i*4 : i*4+4])
	}

	rounds := 6 + 52/n
	sum := rounds * delta
	y := v[0]

	for ; rounds > 0; rounds-- {
		e := (sum >> 2) & 3
		var p uint32
		for p = n - 1; p > 0; p-- {
			z := v[p-1]
			v[p] -= mx(y, z, sum, p, e, &key.data)
			y = v[p]
		}
		z := v[n-1]
		v[0] -= mx(y, z, sum, p, e, &key.data)
		y = v[0]
		sum -= delta
	}

	// Encode back to buffer
	for i := uint32(0); i < n; i++ {
		binary.LittleEndian.PutUint32(buffer[i*4:i*4+4], v[i])
	}

	return nil
}

// Encrypt encrypts input data and writes the result to output.
// The input is padded with zeros to a multiple of 4 bytes if necessary.
// Returns the number of bytes written, or an error.
func Encrypt(key *Key, input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, nil
	}

	// Pad to multiple of 4, minimum 8 bytes
	paddedLen := ((len(input) - 1) | 0x03) + 1
	if paddedLen < 8 {
		paddedLen = 8
	}

	output := make([]byte, paddedLen)
	copy(output, input)
	// Remaining bytes are already zero (Go slice initialization)

	err := EncryptInPlace(key, output)
	if err != nil {
		return nil, err
	}

	return output, nil
}

// Decrypt decrypts input data and writes the result to output.
// The input is padded with zeros to a multiple of 4 bytes if necessary.
// Returns the decrypted bytes, or an error.
func Decrypt(key *Key, input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, nil
	}

	// Pad to multiple of 4, minimum 8 bytes
	paddedLen := ((len(input) - 1) | 0x03) + 1
	if paddedLen < 8 {
		paddedLen = 8
	}

	output := make([]byte, paddedLen)
	copy(output, input)

	err := DecryptInPlace(key, output)
	if err != nil {
		return nil, err
	}

	return output, nil
}
