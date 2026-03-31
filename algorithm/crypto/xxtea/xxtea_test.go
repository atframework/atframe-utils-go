// Copyright 2026 atframework
// Licensed under the MIT licenses.

package xxtea

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Test vectors from the C++ xxtea_test.cpp.
// Key is decoded big-endian (matching xxtea_setup / XXTEA_GET_UINT32_BE).
// Plaintext and ciphertext are raw byte buffers operated on as little-endian
// uint32 arrays (matching C++ reinterpret_cast<uint32_t*>).
var xxteaTestKey = [6][16]byte{
	{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
	{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
	{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
}

var xxteaTestPT = [6][8]byte{
	{0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48},
	{0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41},
	{0x5a, 0x5b, 0x6e, 0x27, 0x89, 0x48, 0xd7, 0x7f},
	{0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48},
	{0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41, 0x41},
	{0x70, 0xe1, 0x22, 0x5d, 0x6e, 0x4e, 0x76, 0x55},
}

var xxteaTestCT = [6][8]byte{
	{0x70, 0x6c, 0xd7, 0x32, 0x3e, 0xd8, 0x60, 0xe8},
	{0x0d, 0x5a, 0x2d, 0x8b, 0x6a, 0x43, 0x18, 0x30},
	{0x0d, 0xa4, 0xba, 0xd3, 0xb4, 0x2a, 0x78, 0x85},
	{0x62, 0xeb, 0x33, 0x08, 0x10, 0x86, 0x0a, 0x17},
	{0xd1, 0xbe, 0xdf, 0x50, 0xdc, 0xf2, 0x90, 0x43},
	{0x47, 0xcc, 0x5f, 0xb9, 0x91, 0x90, 0x66, 0x6b},
}

// TestBasic matches the C++ CASE_TEST(xxtea, basic).
// Encrypt in-place, compare to expected ciphertext.
// Decrypt in-place, compare to original plaintext.
func TestBasic(t *testing.T) {
	for i := 0; i < 6; i++ {
		key, err := Setup(xxteaTestKey[i][:])
		if err != nil {
			t.Fatalf("vector %d: Setup failed: %v", i, err)
		}

		// --- encrypt ---
		buf := make([]byte, 8)
		copy(buf, xxteaTestPT[i][:])
		if err := EncryptInPlace(&key, buf); err != nil {
			t.Fatalf("vector %d: EncryptInPlace failed: %v", i, err)
		}
		if !bytes.Equal(buf, xxteaTestCT[i][:]) {
			t.Errorf("vector %d encrypt: got %x, want %x", i, buf, xxteaTestCT[i])
		}

		// --- decrypt ---
		copy(buf, xxteaTestCT[i][:])
		if err := DecryptInPlace(&key, buf); err != nil {
			t.Fatalf("vector %d: DecryptInPlace failed: %v", i, err)
		}
		if !bytes.Equal(buf, xxteaTestPT[i][:]) {
			t.Errorf("vector %d decrypt: got %x, want %x", i, buf, xxteaTestPT[i])
		}
	}
}

// TestInputOutput matches the C++ CASE_TEST(xxtea, input_output).
// Uses the Encrypt/Decrypt functions with separate input and output buffers.
func TestInputOutput(t *testing.T) {
	for i := 0; i < 6; i++ {
		key, err := Setup(xxteaTestKey[i][:])
		if err != nil {
			t.Fatalf("vector %d: Setup failed: %v", i, err)
		}

		// --- encrypt ---
		ct, err := Encrypt(&key, xxteaTestPT[i][:])
		if err != nil {
			t.Fatalf("vector %d: Encrypt failed: %v", i, err)
		}
		if len(ct) != 8 {
			t.Errorf("vector %d encrypt: output length %d, want 8", i, len(ct))
		}
		if !bytes.Equal(ct, xxteaTestCT[i][:]) {
			t.Errorf("vector %d encrypt: got %x, want %x", i, ct, xxteaTestCT[i])
		}

		// --- decrypt ---
		pt, err := Decrypt(&key, xxteaTestCT[i][:])
		if err != nil {
			t.Fatalf("vector %d: Decrypt failed: %v", i, err)
		}
		if len(pt) != 8 {
			t.Errorf("vector %d decrypt: output length %d, want 8", i, len(pt))
		}
		if !bytes.Equal(pt, xxteaTestPT[i][:]) {
			t.Errorf("vector %d decrypt: got %x, want %x", i, pt, xxteaTestPT[i])
		}
	}
}

// TestRoundTrip verifies encrypt then decrypt recovers the original data.
func TestRoundTrip(t *testing.T) {
	key, err := Setup([]byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF,
		0xFE, 0xDC, 0xBA, 0x98, 0x76, 0x54, 0x32, 0x10,
	})
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	original := []byte("Hello, XXTEA! This is a test message for roundtrip.  ")
	// pad to multiple of 4
	for len(original)%4 != 0 {
		original = append(original, 0)
	}

	buf := make([]byte, len(original))
	copy(buf, original)

	if err := EncryptInPlace(&key, buf); err != nil {
		t.Fatalf("EncryptInPlace failed: %v", err)
	}
	if bytes.Equal(buf, original) {
		t.Error("encrypted data should differ from original")
	}

	if err := DecryptInPlace(&key, buf); err != nil {
		t.Fatalf("DecryptInPlace failed: %v", err)
	}
	if !bytes.Equal(buf, original) {
		t.Errorf("round trip failed: got %x, want %x", buf, original)
	}
}

// TestSetupErrors validates key setup error handling.
func TestSetupErrors(t *testing.T) {
	_, err := Setup([]byte{1, 2, 3})
	if err != ErrInvalidKeySize {
		t.Errorf("expected ErrInvalidKeySize for short key, got %v", err)
	}

	_, err = Setup(make([]byte, 17))
	if err != ErrInvalidKeySize {
		t.Errorf("expected ErrInvalidKeySize for long key, got %v", err)
	}
}

// TestBufferSizeErrors validates buffer size validation.
func TestBufferSizeErrors(t *testing.T) {
	key, _ := Setup(make([]byte, 16))

	// Not a multiple of 4
	err := EncryptInPlace(&key, make([]byte, 5))
	if err != ErrInvalidBufferSize {
		t.Errorf("expected ErrInvalidBufferSize for odd-length, got %v", err)
	}

	// Too small (4 bytes = 1 uint32, need at least 2)
	err = EncryptInPlace(&key, make([]byte, 4))
	if err != ErrInvalidBufferSize {
		t.Errorf("expected ErrInvalidBufferSize for 4-byte buffer, got %v", err)
	}

	// Empty buffer should not error
	err = EncryptInPlace(&key, nil)
	if err != nil {
		t.Errorf("empty buffer should not error, got %v", err)
	}

	err = DecryptInPlace(&key, nil)
	if err != nil {
		t.Errorf("empty buffer should not error, got %v", err)
	}
}

// TestEncryptDecryptPadding verifies automatic padding in Encrypt/Decrypt.
func TestEncryptDecryptPadding(t *testing.T) {
	key, _ := Setup(xxteaTestKey[0][:])

	// 5-byte input → padded to 8 bytes
	input := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	ct, err := Encrypt(&key, input)
	if err != nil {
		t.Fatalf("Encrypt(5 bytes) failed: %v", err)
	}
	if len(ct) != 8 {
		t.Errorf("expected padded length 8, got %d", len(ct))
	}

	pt, err := Decrypt(&key, ct)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	// The first 5 bytes should match; trailing bytes are padding zeros
	if !bytes.Equal(pt[:5], input) {
		t.Errorf("decrypted data[:5] = %x, want %x", pt[:5], input)
	}
}

// TestKeySetupBigEndian verifies that key setup uses big-endian byte decoding,
// matching the C++ XXTEA_GET_UINT32_BE macro.
func TestKeySetupBigEndian(t *testing.T) {
	keyBytes := []byte{
		0x00, 0x01, 0x02, 0x03, // → 0x00010203
		0x04, 0x05, 0x06, 0x07, // → 0x04050607
		0x08, 0x09, 0x0a, 0x0b, // → 0x08090a0b
		0x0c, 0x0d, 0x0e, 0x0f, // → 0x0c0d0e0f
	}

	key, err := Setup(keyBytes)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	expected := [4]uint32{0x00010203, 0x04050607, 0x08090a0b, 0x0c0d0e0f}
	if key.data != expected {
		t.Errorf("key.data = %08x, want %08x", key.data, expected)
	}
}

// TestCrossLanguageVector generates a deterministic vector that can be validated
// against the C++ implementation to confirm cross-language interoperability.
func TestCrossLanguageVector(t *testing.T) {
	// Key: sequential bytes 0x00..0x0f, big-endian decoded
	keyBytes := []byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	}
	key, err := Setup(keyBytes)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Build a 16-byte (4 uint32) test buffer with known little-endian content
	input := make([]byte, 16)
	binary.LittleEndian.PutUint32(input[0:4], 0x01020304)
	binary.LittleEndian.PutUint32(input[4:8], 0x05060708)
	binary.LittleEndian.PutUint32(input[8:12], 0x090A0B0C)
	binary.LittleEndian.PutUint32(input[12:16], 0x0D0E0F10)

	original := make([]byte, 16)
	copy(original, input)

	if err := EncryptInPlace(&key, input); err != nil {
		t.Fatalf("EncryptInPlace failed: %v", err)
	}

	t.Logf("Encrypted: %x", input)

	if err := DecryptInPlace(&key, input); err != nil {
		t.Fatalf("DecryptInPlace failed: %v", err)
	}
	if !bytes.Equal(input, original) {
		t.Errorf("round trip failed: got %x, want %x", input, original)
	}
}

// TestEmptyInputEncryptDecrypt verifies that empty input returns nil.
func TestEmptyInputEncryptDecrypt(t *testing.T) {
	key, _ := Setup(make([]byte, 16))

	ct, err := Encrypt(&key, nil)
	if err != nil || ct != nil {
		t.Errorf("Encrypt(nil) = (%v, %v), want (nil, nil)", ct, err)
	}

	ct, err = Encrypt(&key, []byte{})
	if err != nil || ct != nil {
		t.Errorf("Encrypt([]) = (%v, %v), want (nil, nil)", ct, err)
	}

	pt, err := Decrypt(&key, nil)
	if err != nil || pt != nil {
		t.Errorf("Decrypt(nil) = (%v, %v), want (nil, nil)", pt, err)
	}

	pt, err = Decrypt(&key, []byte{})
	if err != nil || pt != nil {
		t.Errorf("Decrypt([]) = (%v, %v), want (nil, nil)", pt, err)
	}
}
