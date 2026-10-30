package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const maxDisplayNameLength = 64

var (
	ErrDisplayNameRequired = errors.New("display name is required")
	ErrDisplayNameTooLong  = errors.New("display name is too long")
)

type DisplayName struct {
	value string
}

func ParseDisplayName(s string) (DisplayName, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return DisplayName{}, ErrDisplayNameRequired
	}

	if utf8.RuneCountInString(trimmed) > maxDisplayNameLength {
		return DisplayName{}, ErrDisplayNameTooLong
	}

	return DisplayName{value: trimmed}, nil
}

func (n DisplayName) String() string { return n.value }
