package domain

import (
	"errors"
	"regexp"
)

var ErrInvalidSubdomain = errors.New("invalid subdomain")

var (
	subdomainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

	rejectedSubdomainPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^[0-9a-f]{16}$`),
		regexp.MustCompile(`^..--`),
		regexp.MustCompile(`^(www|api|admin|auth)$`),
		regexp.MustCompile(`^s[0-9]+$`),
	}
)

type Subdomain struct {
	value string
}

func ParseSubdomain(s string) (Subdomain, error) {
	if !subdomainPattern.MatchString(s) {
		return Subdomain{}, ErrInvalidSubdomain
	}

	for _, rejected := range rejectedSubdomainPatterns {
		if rejected.MatchString(s) {
			return Subdomain{}, ErrInvalidSubdomain
		}
	}

	return Subdomain{value: s}, nil
}

func (s Subdomain) String() string { return s.value }
