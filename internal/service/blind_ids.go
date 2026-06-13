package service

import (
	"errors"
	"regexp"
)

var blindIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// A Stellar contract (C...) address: 56-char base32 StrKey, uppercase.
var contractAddressPattern = regexp.MustCompile(`^C[A-Z2-7]{55}$`)

var ErrValidation = errors.New("validation error")

func IsBlindID(s string) bool {
	return blindIDPattern.MatchString(s)
}

func IsContractAddress(s string) bool {
	return contractAddressPattern.MatchString(s)
}
