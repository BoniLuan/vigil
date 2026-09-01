package check

import (
	"errors"
	"net/url"
	"strings"
)

// ValidateExecutionURL is a defensive execution-time boundary. Domain
// validation remains authoritative for monitor configuration; this function
// repeats only the properties required before DNS and dialing.
func ValidateExecutionURL(raw string) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return nil, errors.New("invalid destination URL")
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, errors.New("destination URL scheme must be http or https")
	}
	if parsed.User != nil {
		return nil, errors.New("destination URL must not contain credentials")
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("destination URL must contain a hostname")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return parsed, nil
}
