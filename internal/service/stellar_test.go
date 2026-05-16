package service

import (
	"encoding/base64"
	"testing"
)

// TestSymbolToScValXDR verifies the "transfer" XDR against the value the mobile
// client expects when building SAC event topic filters.
func TestSymbolToScValXDR(t *testing.T) {
	tests := []struct {
		sym  string
		want string
	}{
		// Verified against references/latch-mobile SAC event filter constants.
		{"transfer", "AAAADwAAAAh0cmFuc2Zlcg=="},
	}
	for _, tt := range tests {
		got := SymbolToScValXDR(tt.sym)
		if got != tt.want {
			t.Errorf("SymbolToScValXDR(%q) = %q, want %q", tt.sym, got, tt.want)
		}
		// Sanity: decoded bytes must carry SCV_SYMBOL type byte (0x0F).
		raw, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			t.Fatalf("base64 decode of result: %v", err)
		}
		if len(raw) < 8 {
			t.Fatalf("result too short: %d bytes", len(raw))
		}
		if raw[3] != 0x0F {
			t.Errorf("expected SCV_SYMBOL (0x0F), got 0x%02x", raw[3])
		}
	}
}

// TestSymbolToScValXDR_LengthEncoding checks that the string length field is set correctly.
func TestSymbolToScValXDR_LengthEncoding(t *testing.T) {
	sym := "xyz"
	got := SymbolToScValXDR(sym)
	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatal(err)
	}
	// bytes 4-7 are the big-endian uint32 length
	length := int(raw[4])<<24 | int(raw[5])<<16 | int(raw[6])<<8 | int(raw[7])
	if length != len(sym) {
		t.Errorf("encoded length = %d, want %d", length, len(sym))
	}
}

// TestAddressToScValXDR_RoundTrip encodes then decodes G- and C-addresses and
// asserts the original address is recovered.
func TestAddressToScValXDR_RoundTrip(t *testing.T) {
	// Build test addresses with the correct CRC-16 checksum so the round-trip
	// is guaranteed to reproduce the same string.
	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = byte(i + 1) // non-zero distinguishable bytes
	}

	gAddr := encodeStrkey(strkeyVersionAccount, payload)
	cAddr := encodeStrkey(strkeyVersionContract, payload)

	tests := []struct {
		name    string
		address string
	}{
		{"G-address", gAddr},
		{"C-address", cAddr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xdr, err := AddressToScValXDR(tt.address)
			if err != nil {
				t.Fatalf("AddressToScValXDR(%q): %v", tt.address, err)
			}

			raw, err := base64.StdEncoding.DecodeString(xdr)
			if err != nil {
				t.Fatalf("base64 decode: %v", err)
			}
			if raw[3] != 0x12 {
				t.Fatalf("expected SCV_ADDRESS (0x12), got 0x%02x", raw[3])
			}

			got, err := DecodeScAddressTopic(xdr)
			if err != nil {
				t.Fatalf("DecodeScAddressTopic: %v", err)
			}
			if got != tt.address {
				t.Errorf("round-trip failed: got %q, want %q", got, tt.address)
			}
		})
	}
}

// TestAddressToScValXDR_GAddressLayout verifies the exact XDR byte layout for
// a G-address (account): [SCV_ADDRESS][ACCOUNT type][ED25519 type][32-byte key].
func TestAddressToScValXDR_GAddressLayout(t *testing.T) {
	payload := make([]byte, 32)
	payload[0] = 0xAB
	payload[31] = 0xCD

	gAddr := encodeStrkey(strkeyVersionAccount, payload)
	xdrB64, err := AddressToScValXDR(gAddr)
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := base64.StdEncoding.DecodeString(xdrB64)
	if len(raw) != 44 {
		t.Fatalf("G-address XDR len = %d, want 44", len(raw))
	}
	// bytes 0-2: 0x00
	if raw[3] != 0x12 {
		t.Errorf("byte[3] SCV_ADDRESS = 0x%02x, want 0x12", raw[3])
	}
	// bytes 4-7: SC_ADDRESS_TYPE_ACCOUNT = 0
	for i := 4; i <= 7; i++ {
		if raw[i] != 0 {
			t.Errorf("byte[%d] address type = %d, want 0", i, raw[i])
		}
	}
	// bytes 8-11: KEY_TYPE_ED25519 = 0
	for i := 8; i <= 11; i++ {
		if raw[i] != 0 {
			t.Errorf("byte[%d] key type = %d, want 0", i, raw[i])
		}
	}
	// bytes 12-43: key payload
	if raw[12] != 0xAB || raw[43] != 0xCD {
		t.Errorf("key bytes not copied correctly: raw[12]=0x%02x raw[43]=0x%02x", raw[12], raw[43])
	}
}

// TestAddressToScValXDR_CAddressLayout verifies the XDR layout for a C-address
// (contract): [SCV_ADDRESS][CONTRACT type][32-byte contract ID].
func TestAddressToScValXDR_CAddressLayout(t *testing.T) {
	payload := make([]byte, 32)
	payload[0] = 0xDE
	payload[31] = 0xAD

	cAddr := encodeStrkey(strkeyVersionContract, payload)
	xdrB64, err := AddressToScValXDR(cAddr)
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := base64.StdEncoding.DecodeString(xdrB64)
	if len(raw) != 40 {
		t.Fatalf("C-address XDR len = %d, want 40", len(raw))
	}
	if raw[3] != 0x12 {
		t.Errorf("byte[3] SCV_ADDRESS = 0x%02x, want 0x12", raw[3])
	}
	// byte 7 = SC_ADDRESS_TYPE_CONTRACT = 1
	if raw[7] != 0x01 {
		t.Errorf("byte[7] contract type = 0x%02x, want 0x01", raw[7])
	}
	if raw[8] != 0xDE || raw[39] != 0xAD {
		t.Errorf("contract ID bytes not copied: raw[8]=0x%02x raw[39]=0x%02x", raw[8], raw[39])
	}
}

// TestAddressToScValXDR_Errors verifies that invalid inputs return errors.
func TestAddressToScValXDR_Errors(t *testing.T) {
	tests := []struct {
		name    string
		address string
	}{
		{"empty", ""},
		{"bad prefix", "XAAAA"},
		{"not base32", "!!!!!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := AddressToScValXDR(tt.address)
			if err == nil {
				t.Errorf("AddressToScValXDR(%q) expected error, got nil", tt.address)
			}
		})
	}
}

// TestDecodeI128Amount verifies stroops → XLM conversion.
func TestDecodeI128Amount(t *testing.T) {
	buildI128XDR := func(lo uint64) string {
		raw := make([]byte, 20)
		raw[3] = 0x0A // SCV_I128
		// hi = 0 (bytes 4–11 already zero)
		// lo as big-endian uint64 in bytes 12–19
		for i := 0; i < 8; i++ {
			raw[19-i] = byte(lo >> (8 * uint(i)))
		}
		return base64.StdEncoding.EncodeToString(raw)
	}

	tests := []struct {
		name    string
		stroops uint64
		wantAmt string
	}{
		{"zero", 0, "0.0000000"},
		{"one stroop", 1, "0.0000001"},
		{"one XLM", 10_000_000, "1.0000000"},
		{"100 XLM", 1_000_000_000, "100.0000000"},
		{"fractional", 12_345_678, "1.2345678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xdr := buildI128XDR(tt.stroops)
			got, err := DecodeI128Amount(xdr)
			if err != nil {
				t.Fatalf("DecodeI128Amount: %v", err)
			}
			if got != tt.wantAmt {
				t.Errorf("got %q, want %q", got, tt.wantAmt)
			}
		})
	}
}

// TestDecodeI128Amount_NegativeReturnsZero verifies negative hi → "0".
func TestDecodeI128Amount_NegativeReturnsZero(t *testing.T) {
	raw := make([]byte, 20)
	raw[3] = 0x0A
	raw[4] = 0xFF // negative hi (sign bit set)
	xdr := base64.StdEncoding.EncodeToString(raw)
	got, err := DecodeI128Amount(xdr)
	if err != nil {
		t.Fatal(err)
	}
	if got != "0" {
		t.Errorf("negative i128 got %q, want 0", got)
	}
}

// TestDecodeI128Amount_WrongType verifies that a non-i128 XDR returns an error.
func TestDecodeI128Amount_WrongType(t *testing.T) {
	raw := make([]byte, 20)
	raw[3] = 0x0F // SCV_SYMBOL, not SCV_I128
	xdr := base64.StdEncoding.EncodeToString(raw)
	_, err := DecodeI128Amount(xdr)
	if err == nil {
		t.Error("expected error for wrong ScVal type, got nil")
	}
}
