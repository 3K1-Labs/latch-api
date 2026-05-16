package service

import (
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// stellarBase32 is standard RFC-4648 base32 without padding, as used by Stellar strkey.
var stellarBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// Stellar strkey encodes the address type in the high 5 bits of the version byte,
// so that the first base32 character is recognisable: G for accounts, C for contracts.
// Version byte = enum_value << 3. Account enum = 6 (6>>3=0 is wrong; 6<<3=48 gives 'G').
const (
	strkeyVersionAccount  byte = 6 << 3 // 48  → 'G' (Ed25519 public key)
	strkeyVersionContract byte = 2 << 3 // 16  → 'C' (Soroban contract ID)
)

// strkeyRawBytes decodes a Stellar strkey (G..., C...) and returns the raw 32-byte payload,
// stripping the version byte and the 2-byte CRC-16 checksum.
func strkeyRawBytes(address string) ([]byte, error) {
	decoded, err := stellarBase32.DecodeString(strings.ToUpper(address))
	if err != nil {
		return nil, fmt.Errorf("base32 decode: %w", err)
	}
	// Layout: [1 version] [32 payload] [2 checksum] = 35 bytes
	if len(decoded) < 35 {
		return nil, fmt.Errorf("strkey too short (%d bytes)", len(decoded))
	}
	return decoded[1 : len(decoded)-2], nil
}

// crc16 computes CRC-16/XMODEM, used by Stellar strkey checksums.
func crc16(data []byte) uint16 {
	const poly = 0x1021
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// encodeStrkey encodes raw bytes as a Stellar strkey with the given version byte.
func encodeStrkey(version byte, payload []byte) string {
	data := make([]byte, 0, 1+len(payload)+2)
	data = append(data, version)
	data = append(data, payload...)
	checksum := crc16(data)
	data = append(data, byte(checksum), byte(checksum>>8)) // little-endian
	return stellarBase32.EncodeToString(data)
}

// AddressToScValXDR converts a G... (account) or C... (contract) Stellar address to a
// base64-encoded XDR ScVal of type SCV_ADDRESS (18), for use as a Soroban event topic filter.
//
// XDR layout for account (44 bytes):
//
//	[00 00 00 12] SCV_ADDRESS
//	[00 00 00 00] SC_ADDRESS_TYPE_ACCOUNT
//	[00 00 00 00] KEY_TYPE_ED25519
//	[32 bytes]    ed25519 public key
//
// XDR layout for contract (40 bytes):
//
//	[00 00 00 12] SCV_ADDRESS
//	[00 00 00 01] SC_ADDRESS_TYPE_CONTRACT
//	[32 bytes]    contract ID
func AddressToScValXDR(address string) (string, error) {
	raw, err := strkeyRawBytes(address)
	if err != nil {
		return "", fmt.Errorf("decode %q: %w", address, err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("expected 32-byte payload for %q, got %d", address, len(raw))
	}

	const scvAddress = byte(0x12) // SCV_ADDRESS = 18

	var buf []byte
	switch {
	case strings.HasPrefix(address, "G"):
		buf = make([]byte, 44)
		buf[3] = scvAddress
		// [4-7]: SC_ADDRESS_TYPE_ACCOUNT = 0
		// [8-11]: KEY_TYPE_ED25519 = 0
		copy(buf[12:], raw)
	case strings.HasPrefix(address, "C"):
		buf = make([]byte, 40)
		buf[3] = scvAddress
		buf[7] = 0x01 // SC_ADDRESS_TYPE_CONTRACT = 1
		copy(buf[8:], raw)
	default:
		return "", fmt.Errorf("unsupported address prefix in %q", address)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// SymbolToScValXDR encodes a string as a base64 XDR ScVal of type SCV_SYMBOL (15).
// Used to build the "transfer" topic filter for Soroban SAC events.
func SymbolToScValXDR(sym string) string {
	b := []byte(sym)
	padLen := (4 - len(b)%4) % 4
	buf := make([]byte, 8+len(b)+padLen)
	buf[3] = 0x0F // SCV_SYMBOL = 15
	l := len(b)
	buf[4] = byte(l >> 24)
	buf[5] = byte(l >> 16)
	buf[6] = byte(l >> 8)
	buf[7] = byte(l)
	copy(buf[8:], b)
	return base64.StdEncoding.EncodeToString(buf)
}

// DecodeScAddressTopic decodes a base64 XDR ScVal(SCV_ADDRESS) topic and returns
// the Stellar address string (G... or C...).
func DecodeScAddressTopic(xdrBase64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(xdrBase64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	if len(raw) < 12 {
		return "", fmt.Errorf("XDR too short: %d bytes", len(raw))
	}
	if raw[3] != 0x12 {
		return "", fmt.Errorf("expected SCV_ADDRESS (0x12), got 0x%02x", raw[3])
	}
	addrType := raw[7]
	switch addrType {
	case 0: // SC_ADDRESS_TYPE_ACCOUNT: [8-11] KEY_TYPE_ED25519, [12-43] key
		if len(raw) < 44 {
			return "", fmt.Errorf("account ScVal too short: %d bytes", len(raw))
		}
		return encodeStrkey(strkeyVersionAccount, raw[12:44]), nil
	case 1: // SC_ADDRESS_TYPE_CONTRACT: [8-39] contract ID
		if len(raw) < 40 {
			return "", fmt.Errorf("contract ScVal too short: %d bytes", len(raw))
		}
		return encodeStrkey(strkeyVersionContract, raw[8:40]), nil
	default:
		return "", fmt.Errorf("unknown ScAddressType: %d", addrType)
	}
}

// DecodeI128Amount decodes a base64 XDR ScVal(SCV_I128) and returns the value
// divided by 10,000,000 (stroops → XLM), formatted as a 7-decimal string.
func DecodeI128Amount(xdrBase64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(xdrBase64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	// SCV_I128 = 10 (0x0A): [0-3] type, [4-11] hi int64, [12-19] lo uint64
	if len(raw) < 20 {
		return "", fmt.Errorf("i128 ScVal too short: %d bytes", len(raw))
	}
	if raw[3] != 0x0A {
		return "", fmt.Errorf("expected SCV_I128 (0x0A), got 0x%02x", raw[3])
	}
	hi := int64(raw[4])<<56 | int64(raw[5])<<48 | int64(raw[6])<<40 | int64(raw[7])<<32 |
		int64(raw[8])<<24 | int64(raw[9])<<16 | int64(raw[10])<<8 | int64(raw[11])
	lo := uint64(raw[12])<<56 | uint64(raw[13])<<48 | uint64(raw[14])<<40 | uint64(raw[15])<<32 |
		uint64(raw[16])<<24 | uint64(raw[17])<<16 | uint64(raw[18])<<8 | uint64(raw[19])

	if hi < 0 {
		return "0", nil // negative amounts are not expected for SAC transfers
	}
	// For all practical XLM amounts, hi == 0.
	const stroopsPerXLM = 10_000_000
	if hi == 0 {
		xlm := float64(lo) / stroopsPerXLM
		return strconv.FormatFloat(xlm, 'f', 7, 64), nil
	}
	// hi > 0: extremely large amount — return stroops as a string
	return fmt.Sprintf("%d%019d", hi, lo), nil
}
