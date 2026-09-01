package monitor

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return "monitor configuration is invalid" }

func NormalizeSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	separator := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if r <= unicode.MaxASCII {
				if separator && result.Len() > 0 {
					result.WriteByte('-')
				}
				result.WriteRune(r)
				separator = false
			}
			continue
		}
		separator = true
	}
	return result.String()
}

func prepareCreate(input CreateInput) (Monitor, error) {
	name := strings.TrimSpace(input.Name)
	slug := input.Slug
	if strings.TrimSpace(slug) == "" {
		slug = name
	}
	slug = NormalizeSlug(slug)

	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	if kind == "" {
		kind = KindHTTP
	}
	method := strings.ToUpper(strings.TrimSpace(input.HTTPMethod))
	if method == "" {
		method = MethodGET
	}
	if input.ExpectedStatusMin == 0 {
		input.ExpectedStatusMin = 200
	}
	if input.ExpectedStatusMax == 0 {
		input.ExpectedStatusMax = 200
	}
	if input.Interval == 0 {
		input.Interval = 60 * time.Second
	}
	if input.Timeout == 0 {
		input.Timeout = 2 * time.Second
	}
	if input.FailureThreshold == 0 {
		input.FailureThreshold = 3
	}
	if input.RecoveryThreshold == 0 {
		input.RecoveryThreshold = 1
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	description := normalizeDescription(input.Description)

	model := Monitor{
		Name: name, Slug: slug, Description: description, Kind: kind,
		URL: strings.TrimSpace(input.URL), HTTPMethod: method,
		ExpectedStatusMin: input.ExpectedStatusMin, ExpectedStatusMax: input.ExpectedStatusMax,
		Interval: input.Interval, Timeout: input.Timeout,
		FailureThreshold: input.FailureThreshold, RecoveryThreshold: input.RecoveryThreshold,
		Enabled: enabled, Public: input.Public,
	}
	if err := Validate(model); err != nil {
		return Monitor{}, err
	}
	return model, nil
}

func Validate(value Monitor) error {
	fields := make(map[string]string)
	if value.Name == "" {
		fields["name"] = "must not be empty"
	} else if len(value.Name) > 200 {
		fields["name"] = "must be at most 200 characters"
	}
	if value.Slug == "" || len(value.Slug) > 100 || !slugPattern.MatchString(value.Slug) {
		fields["slug"] = "must contain lowercase letters or digits separated by single hyphens"
	}
	if value.Kind != KindHTTP {
		fields["kind"] = "must be http"
	}
	if value.HTTPMethod != MethodGET && value.HTTPMethod != MethodHEAD {
		fields["http_method"] = "must be GET or HEAD"
	}
	if err := validateStaticURL(value.URL); err != nil {
		fields["url"] = err.Error()
	}
	if value.ExpectedStatusMin < 100 || value.ExpectedStatusMin > 599 {
		fields["expected_status_min"] = "must be between 100 and 599"
	}
	if value.ExpectedStatusMax < 100 || value.ExpectedStatusMax > 599 {
		fields["expected_status_max"] = "must be between 100 and 599"
	} else if value.ExpectedStatusMin > value.ExpectedStatusMax {
		fields["expected_status_max"] = "must be greater than or equal to expected_status_min"
	}
	if value.Interval < MinInterval || value.Interval > MaxInterval {
		fields["interval_seconds"] = fmt.Sprintf("must be between %d and %d seconds", int(MinInterval.Seconds()), int(MaxInterval.Seconds()))
	}
	if value.Timeout < MinTimeout || value.Timeout > MaxTimeout {
		fields["timeout_ms"] = fmt.Sprintf("must be between %d and %d milliseconds", MinTimeout.Milliseconds(), MaxTimeout.Milliseconds())
	} else if value.Timeout >= value.Interval {
		fields["timeout_ms"] = "must be less than interval_seconds"
	}
	if value.FailureThreshold < 1 || value.FailureThreshold > 100 {
		fields["failure_threshold"] = "must be between 1 and 100"
	}
	if value.RecoveryThreshold < 1 || value.RecoveryThreshold > 100 {
		fields["recovery_threshold"] = "must be between 1 and 100"
	}
	if value.Description != nil && len(*value.Description) > 2000 {
		fields["description"] = "must be at most 2000 characters"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func ValidateState(state string) error {
	switch state {
	case StatePending, StateUp, StateDown, StatePaused:
		return nil
	default:
		return fmt.Errorf("unsupported monitor state %q", state)
	}
}

func validateStaticURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" {
		return errors.New("must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if parsed.User != nil {
		return errors.New("must not contain embedded credentials")
	}
	if parsed.Fragment != "" {
		return errors.New("must not contain a fragment")
	}
	return nil
}

func normalizeDescription(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
