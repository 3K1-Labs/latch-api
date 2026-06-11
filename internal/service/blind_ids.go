package service

import (
	"errors"
	"regexp"
)

var blindIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var ErrValidation = errors.New("validation error")

func IsBlindID(s string) bool {
	return blindIDPattern.MatchString(s)
}
