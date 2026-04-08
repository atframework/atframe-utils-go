// Copyright 2026 atframework
// Licensed under the MIT licenses.

package chacha20

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test vectors extracted from libsodium test/default/chacha20.c (tv() function)
// and test/default/chacha20.exp (expected output).
//
// Each vector: key (32 bytes), nonce (8 bytes), initial counter = 0,
// expected keystream = first 160 bytes of crypto_stream_chacha20(out, 160, nonce, key).

func TestXORKeyStreamLibsodiumVectors(t *testing.T) {
	tests := []struct {
		name     string
		keyHex   string
		nonceHex string
		// Expected 160-byte keystream (hex) — the output of crypto_stream_chacha20 with 160 zero bytes
		expectedHex string
	}{
		{
			name:     "vector1_all_zeros",
			keyHex:   "0000000000000000000000000000000000000000000000000000000000000000",
			nonceHex: "0000000000000000",
			expectedHex: "76b8e0ada0f13d90405d6ae55386bd28bdd219b8a08ded1aa836efcc8b770dc7" +
				"da41597c5157488d7724e03fb8d84a376a43b8f41518a11cc387b669b2ee6586" +
				"9f07e7be5551387a98ba977c732d080dcb0f29a048e3656912c6533e32ee7aed" +
				"29b721769ce64e43d57133b074d839d531ed1f28510afb45ace10a1f4b794d6f" +
				"2d09a0e663266ce1ae7ed1081968a0758e718e997bd362c6b0c34634a9a0b35d",
		},
		{
			name:     "vector2_key_lsb_set",
			keyHex:   "0000000000000000000000000000000000000000000000000000000000000001",
			nonceHex: "0000000000000000",
			expectedHex: "4540f05a9f1fb296d7736e7b208e3c96eb4fe1834688d2604f450952ed432d41" +
				"bbe2a0b6ea7566d2a5d1e7e20d42af2c53d792b1c43fea817e9ad275ae546963" +
				"3aeb5224ecf849929b9d828db1ced4dd832025e8018b8160b82284f3c949aa5a" +
				"8eca00bbb4a73bdad192b5c42f73f2fd4e273644c8b36125a64addeb006c13a0" +
				"96d68b9ff7b57e7090f880392effd5b297a83bbaf2fbe8cf5d4618965e3dc776",
		},
		{
			name:     "vector3_nonce_lsb_set",
			keyHex:   "0000000000000000000000000000000000000000000000000000000000000000",
			nonceHex: "0000000000000001",
			expectedHex: "de9cba7bf3d69ef5e786dc63973f653a0b49e015adbff7134fcb7df137821031" +
				"e85a050278a7084527214f73efc7fa5b5277062eb7a0433e445f41e31afab757" +
				"283547e3d3d30ee0371c1e6025ff4c91b794a291cf7568d48ff84b37329e2730" +
				"b12738a072a2b2c7169e326fe4893a7b2421bb910b79599a7ce4fbaee86be427" +
				"c5ee0e8225eb6f48231fd504939d59eac8bd106cc138779b893c54da8758f62a",
		},
		{
			name:     "vector4_nonce_msb_set",
			keyHex:   "0000000000000000000000000000000000000000000000000000000000000000",
			nonceHex: "0100000000000000",
			expectedHex: "ef3fdfd6c61578fbf5cf35bd3dd33b8009631634d21e42ac33960bd138e50d32" +
				"111e4caf237ee53ca8ad6426194a88545ddc497a0b466e7d6bbdb0041b2f586b" +
				"5305e5e44aff19b235936144675efbe4409eb7e8e5f1430f5f5836aeb49bb532" +
				"8b017c4b9dc11f8a03863fa803dc71d5726b2b6b31aa32708afe5af1d6b69058" +
				"4d58792b271e5fdb92c486051c48b79a4d48a109bb2d0477956e74c25e93c3c2",
		},
		{
			name:     "vector5_sequential_key_nonce",
			keyHex:   "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			nonceHex: "0001020304050607",
			expectedHex: "f798a189f195e66982105ffb640bb7757f579da31602fc93ec01ac56f85ac3c1" +
				"34a4547b733b46413042c9440049176905d3be59ea1c53f15916155c2be8241a" +
				"38008b9a26bc35941e2444177c8ade6689de95264986d95889fb60e84629c9bd" +
				"9a5acb1cc118be563eb9b3a4a472f82e09a7e778492b562ef7130e88dfe031c7" +
				"9db9d4f7c7a899151b9a475032b63fc385245fe054e3dd5a97a5f576fe064025",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, err := hex.DecodeString(tc.keyHex)
			require.NoError(t, err)
			nonce, err := hex.DecodeString(tc.nonceHex)
			require.NoError(t, err)
			expected, err := hex.DecodeString(tc.expectedHex)
			require.NoError(t, err)

			// Build IV: [counter=0 (8 bytes LE) | nonce (8 bytes)]
			iv := make([]byte, IVSize)
			copy(iv[8:], nonce)

			// Encrypt 160 zero bytes => should produce the keystream
			src := make([]byte, 160)
			dst := make([]byte, 160)
			err = XORKeyStream(dst, src, key, iv)
			require.NoError(t, err)
			assert.Equal(t, expected, dst, "keystream mismatch for %s", tc.name)
		})
	}
}

// Test initial counter (ic) support, extracted from libsodium's crypto_stream_chacha20_xor_ic test.
// Uses vector 5 with ic=1: the keystream should start from block 1 of the full keystream.
func TestXORKeyStreamWithInitialCounter(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	nonce, _ := hex.DecodeString("0001020304050607")

	// Full 160-byte keystream from vector 5 (ic=0)
	fullKeystream, _ := hex.DecodeString(
		"f798a189f195e66982105ffb640bb7757f579da31602fc93ec01ac56f85ac3c1" +
			"34a4547b733b46413042c9440049176905d3be59ea1c53f15916155c2be8241a" +
			"38008b9a26bc35941e2444177c8ade6689de95264986d95889fb60e84629c9bd" +
			"9a5acb1cc118be563eb9b3a4a472f82e09a7e778492b562ef7130e88dfe031c7" +
			"9db9d4f7c7a899151b9a475032b63fc385245fe054e3dd5a97a5f576fe064025")

	// With ic=1, keystream should start at byte 64 (block 1) of the full keystream
	expectedIC1 := fullKeystream[64:] // 96 bytes: block 1 + block 2

	// Build IV with counter=1
	iv := make([]byte, IVSize)
	binary.LittleEndian.PutUint64(iv[:8], 1)
	copy(iv[8:], nonce)

	src := make([]byte, len(expectedIC1))
	dst := make([]byte, len(expectedIC1))
	err := XORKeyStream(dst, src, key, iv)
	require.NoError(t, err)
	assert.Equal(t, expectedIC1, dst, "keystream with ic=1 should match blocks 1..2 of full keystream")
}

// Test that XOR is its own inverse: encrypt(encrypt(data, key, iv), key, iv) == data.
func TestXORKeyStreamRoundTrip(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")

	iv := make([]byte, IVSize)
	iv[8] = 0x42 // nonce byte

	plaintext := []byte("The quick brown fox jumps over the lazy dog. ChaCha20 DJB test data!")
	ciphertext := make([]byte, len(plaintext))
	recovered := make([]byte, len(plaintext))

	err := XORKeyStream(ciphertext, plaintext, key, iv)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(plaintext, ciphertext), "ciphertext should differ from plaintext")

	err = XORKeyStream(recovered, ciphertext, key, iv)
	require.NoError(t, err)
	assert.Equal(t, plaintext, recovered, "decrypt should recover plaintext")
}

// Test in-place encryption (dst == src aliasing).
func TestXORKeyStreamInPlace(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	nonce, _ := hex.DecodeString("0001020304050607")

	iv := make([]byte, IVSize)
	copy(iv[8:], nonce)

	// Encrypt zeros out-of-place to get expected keystream
	src := make([]byte, 64)
	expected := make([]byte, 64)
	err := XORKeyStream(expected, src, key, iv)
	require.NoError(t, err)

	// Encrypt zeros in-place
	inplace := make([]byte, 64)
	err = XORKeyStream(inplace, inplace, key, iv)
	require.NoError(t, err)
	assert.Equal(t, expected, inplace, "in-place should produce same result as out-of-place")
}

// Test multi-block correctness: encrypt a buffer larger than 64 bytes and verify
// it matches the libsodium keystream for vector 5.
func TestXORKeyStreamMultiBlock(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	nonce, _ := hex.DecodeString("0001020304050607")

	fullKeystream, _ := hex.DecodeString(
		"f798a189f195e66982105ffb640bb7757f579da31602fc93ec01ac56f85ac3c1" +
			"34a4547b733b46413042c9440049176905d3be59ea1c53f15916155c2be8241a" +
			"38008b9a26bc35941e2444177c8ade6689de95264986d95889fb60e84629c9bd" +
			"9a5acb1cc118be563eb9b3a4a472f82e09a7e778492b562ef7130e88dfe031c7" +
			"9db9d4f7c7a899151b9a475032b63fc385245fe054e3dd5a97a5f576fe064025")

	iv := make([]byte, IVSize)
	copy(iv[8:], nonce)

	// Test various lengths spanning block boundaries
	for _, length := range []int{1, 32, 63, 64, 65, 96, 127, 128, 129, 160} {
		t.Run("len_"+string(rune('0'+length/100))+string(rune('0'+(length%100)/10))+string(rune('0'+length%10)), func(t *testing.T) {
			src := make([]byte, length)
			dst := make([]byte, length)
			err := XORKeyStream(dst, src, key, iv)
			require.NoError(t, err)
			assert.Equal(t, fullKeystream[:length], dst, "keystream mismatch at length %d", length)
		})
	}
}

// Test XOR with known plaintext, matching libsodium's crypto_stream_chacha20_xor behavior.
// libsodium fills 160 bytes with 0x42, then XORs with keystream(key5, nonce5, ic=0).
func TestXORKeyStreamWithNonZeroInput(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	nonce, _ := hex.DecodeString("0001020304050607")

	// Expected from libsodium: memset(out, 0x42, 160); crypto_stream_chacha20_xor(out, out, 160, nonce, key)
	expectedHex := "b5dae3cbb3d7a42bc0521db92649f5373d15dfe15440bed1ae43ee14ba18818376e616393179040372008b06420b552b4791fc1ba85e11b31b54571e69aa66587a42c9d864fe77d65c6606553ec89c24cb9cd7640bc49b1acbb922aa046b8bffd818895e835afc147cfbf1e6e630ba6c4be5a53a0b69146cb5514cca9da27385dffb96b585eadb5759d8051270f47d81c7661da216a19f18d5e7b734bc440267"
	expected, _ := hex.DecodeString(expectedHex)

	iv := make([]byte, IVSize)
	copy(iv[8:], nonce)

	// Fill with 0x42
	src := make([]byte, 160)
	for i := range src {
		src[i] = 0x42
	}
	dst := make([]byte, 160)

	err := XORKeyStream(dst, src, key, iv)
	require.NoError(t, err)
	assert.Equal(t, expected, dst, "XOR with 0x42 fill should match libsodium output")
}

// Test error handling: invalid key size.
func TestXORKeyStreamInvalidKeySize(t *testing.T) {
	iv := make([]byte, IVSize)
	src := make([]byte, 64)
	dst := make([]byte, 64)

	for _, keyLen := range []int{0, 16, 24, 31, 33, 48, 64} {
		key := make([]byte, keyLen)
		err := XORKeyStream(dst, src, key, iv)
		assert.Error(t, err, "should reject key length %d", keyLen)
		assert.ErrorIs(t, err, ErrInvalidKeySize)
	}
}

// Test error handling: invalid IV size.
func TestXORKeyStreamInvalidIVSize(t *testing.T) {
	key := make([]byte, KeySize)
	src := make([]byte, 64)
	dst := make([]byte, 64)

	for _, ivLen := range []int{0, 8, 12, 15, 17, 24, 32} {
		iv := make([]byte, ivLen)
		err := XORKeyStream(dst, src, key, iv)
		assert.Error(t, err, "should reject IV length %d", ivLen)
		assert.ErrorIs(t, err, ErrInvalidIVSize)
	}
}

// Test error handling: dst too small.
func TestXORKeyStreamDstTooSmall(t *testing.T) {
	key := make([]byte, KeySize)
	iv := make([]byte, IVSize)
	src := make([]byte, 64)
	dst := make([]byte, 32) // smaller than src

	err := XORKeyStream(dst, src, key, iv)
	assert.Error(t, err, "should reject dst smaller than src")
	assert.ErrorIs(t, err, ErrOutputTooSmall)
}

// Test zero-length input.
func TestXORKeyStreamEmptyInput(t *testing.T) {
	key := make([]byte, KeySize)
	iv := make([]byte, IVSize)

	err := XORKeyStream(nil, nil, key, iv)
	require.NoError(t, err)

	err = XORKeyStream([]byte{}, []byte{}, key, iv)
	require.NoError(t, err)
}

// Test with large counter value to ensure 64-bit counter works.
func TestXORKeyStreamLargeCounter(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	nonce, _ := hex.DecodeString("0001020304050607")

	// Counter = 0xFFFFFFFF (tests upper 32 bits of the 64-bit counter)
	iv := make([]byte, IVSize)
	binary.LittleEndian.PutUint64(iv[:8], 0xFFFFFFFF)
	copy(iv[8:], nonce)

	src := make([]byte, 128) // 2 blocks to test counter increment across 32-bit boundary
	dst := make([]byte, 128)
	err := XORKeyStream(dst, src, key, iv)
	require.NoError(t, err)

	// Verify it's not all zeros (i.e., some keystream was generated)
	allZero := true
	for _, b := range dst {
		if b != 0 {
			allZero = false
			break
		}
	}
	assert.False(t, allZero, "keystream should not be all zeros")

	// Verify round-trip at this counter value
	recovered := make([]byte, 128)
	err = XORKeyStream(recovered, dst, key, iv)
	require.NoError(t, err)
	assert.Equal(t, src, recovered, "round-trip should recover zeros")
}
