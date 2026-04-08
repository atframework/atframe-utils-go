// Copyright 2026 atframework
// Licensed under the MIT licenses.

// Package chacha20 implements the original DJB ChaCha20 stream cipher,
// compatible with libsodium's crypto_stream_chacha20_xor_ic.
//
// Go's golang.org/x/crypto/chacha20 only provides the IETF variant
// (12-byte nonce, 32-bit counter). The original DJB variant uses an
// 8-byte nonce and a 64-bit counter, which is a different algorithm.
//
// IV layout (16 bytes total):
//
//	[0..7]  : initial counter (little-endian uint64)
//	[8..15] : nonce (8 bytes)
package chacha20

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

const (
	// NonceSize is the nonce size for the original DJB ChaCha20 variant.
	NonceSize = 8
	// IVSize is the full IV size: 8-byte counter + 8-byte nonce.
	IVSize = 16
	// KeySize is the key size for ChaCha20.
	KeySize = 32
)

var (
	ErrInvalidKeySize = errors.New("chacha20: invalid key size")
	ErrInvalidIVSize  = errors.New("chacha20: invalid iv size")
	ErrOutputTooSmall = errors.New("chacha20: output buffer too small")
)

// XORKeyStream encrypts or decrypts src into dst using the original DJB ChaCha20.
// key must be 32 bytes. iv must be 16 bytes: [counter(8 LE) | nonce(8)].
// dst and src must have the same length; dst may alias src.
func XORKeyStream(dst, src, key, iv []byte) error {
	if len(key) != KeySize {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidKeySize, KeySize, len(key))
	}
	if len(iv) != IVSize {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidIVSize, IVSize, len(iv))
	}
	if len(dst) < len(src) {
		return fmt.Errorf("%w: dst length %d < src length %d", ErrOutputTooSmall, len(dst), len(src))
	}

	counter := binary.LittleEndian.Uint64(iv[:8])
	nonce := iv[8:16]

	// Build the initial state:
	//   [0..3]   : "expand 32-byte k"
	//   [4..11]  : key (8 × uint32 LE)
	//   [12..13] : counter (uint64 LE as 2 × uint32)
	//   [14..15] : nonce (8 bytes as 2 × uint32 LE)
	var state [16]uint32
	state[0] = 0x61707865 // "expa"
	state[1] = 0x3320646e // "nd 3"
	state[2] = 0x79622d32 // "2-by"
	state[3] = 0x6b206574 // "te k"
	for i := 0; i < 8; i++ {
		state[4+i] = binary.LittleEndian.Uint32(key[i*4:])
	}
	state[14] = binary.LittleEndian.Uint32(nonce[0:4])
	state[15] = binary.LittleEndian.Uint32(nonce[4:8])

	var block [64]byte
	for len(src) > 0 {
		state[12] = uint32(counter)
		state[13] = uint32(counter >> 32)

		computeBlock(&block, &state)

		n := len(src)
		if n > 64 {
			n = 64
		}
		for i := 0; i < n; i++ {
			dst[i] = src[i] ^ block[i]
		}

		src = src[n:]
		dst = dst[n:]
		counter++
	}
	return nil
}

// computeBlock computes one 64-byte ChaCha20 block (20 rounds).
func computeBlock(out *[64]byte, state *[16]uint32) {
	var x [16]uint32
	copy(x[:], state[:])

	for i := 0; i < 10; i++ {
		// Column rounds
		quarterRound(&x[0], &x[4], &x[8], &x[12])
		quarterRound(&x[1], &x[5], &x[9], &x[13])
		quarterRound(&x[2], &x[6], &x[10], &x[14])
		quarterRound(&x[3], &x[7], &x[11], &x[15])
		// Diagonal rounds
		quarterRound(&x[0], &x[5], &x[10], &x[15])
		quarterRound(&x[1], &x[6], &x[11], &x[12])
		quarterRound(&x[2], &x[7], &x[8], &x[13])
		quarterRound(&x[3], &x[4], &x[9], &x[14])
	}

	for i := range x {
		binary.LittleEndian.PutUint32(out[i*4:], x[i]+state[i])
	}
}

// quarterRound is the ChaCha20 quarter-round function.
func quarterRound(a, b, c, d *uint32) {
	*a += *b
	*d ^= *a
	*d = bits.RotateLeft32(*d, 16)
	*c += *d
	*b ^= *c
	*b = bits.RotateLeft32(*b, 12)
	*a += *b
	*d ^= *a
	*d = bits.RotateLeft32(*d, 8)
	*c += *d
	*b ^= *c
	*b = bits.RotateLeft32(*b, 7)
}
