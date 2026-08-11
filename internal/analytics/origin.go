package analytics

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Every value here ends up inside a Content-Security-Policy response header and
// inside a generated <script src>. A space or a semicolon would let an
// administrator rewrite the whole policy — including turning it off — so origins
// are validated to a narrow shape rather than escaped.

const maxOriginLength = 253 + 16 // hostname limit plus scheme, port and wildcard

// cspForbidden lists characters that would terminate a directive or a source
// expression if they reached the header.
const cspForbidden = " \t\r\n;,'\"`\\<>(){}|^"

// ParseOrigin accepts a scheme + host [+ port] origin, optionally with a single
// leading "*." to cover subdomains, and returns it normalised. Paths, queries,
// credentials and anything that could break out of a CSP source are rejected.
func ParseOrigin(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("origin is required")
	}
	if len(value) > maxOriginLength {
		return "", fmt.Errorf("origin must be %d characters or fewer", maxOriginLength)
	}
	if strings.ContainsAny(value, cspForbidden) {
		return "", errors.New("origin contains a character that is not allowed in a security policy")
	}
	// A wildcard is only meaningful directly after the scheme.
	wildcard := false
	parsed, err := url.Parse(value)
	if err != nil {
		return "", errors.New("origin must be a valid URL such as https://matomo.example.com")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("origin must start with http:// or https://")
	}
	if parsed.User != nil {
		return "", errors.New("origin must not contain credentials")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("origin must not contain a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must not contain a query or fragment")
	}
	host := strings.ToLower(parsed.Host)
	if host == "" {
		return "", errors.New("origin must include a host")
	}
	if strings.HasPrefix(host, "*.") {
		wildcard = true
		host = strings.TrimPrefix(host, "*.")
	}
	if strings.Contains(host, "*") {
		return "", errors.New("a wildcard is only allowed as a leading *. label")
	}
	name, port := host, ""
	if index := strings.LastIndex(host, ":"); index != -1 && !strings.Contains(host[index:], "]") {
		name, port = host[:index], host[index+1:]
		for _, char := range port {
			if char < '0' || char > '9' {
				return "", errors.New("port must be numeric")
			}
		}
		if port == "" {
			return "", errors.New("port must be numeric")
		}
	}
	if err := validHostname(name); err != nil {
		return "", err
	}
	normalized := scheme + "://"
	if wildcard {
		normalized += "*."
	}
	normalized += name
	if port != "" {
		normalized += ":" + port
	}
	return normalized, nil
}

// validHostname accepts DNS names and bracketed IPv6 literals, and rejects
// anything else so the header cannot receive an unexpected token.
func validHostname(name string) error {
	if name == "" {
		return errors.New("origin must include a host")
	}
	if strings.HasPrefix(name, "[") {
		if !strings.HasSuffix(name, "]") {
			return errors.New("IPv6 host must be enclosed in brackets")
		}
		for _, char := range name[1 : len(name)-1] {
			if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char == ':' || char == '.') {
				return errors.New("IPv6 host contains an unexpected character")
			}
		}
		return nil
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return errors.New("host contains an empty label")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return errors.New("host label must be between 1 and 63 characters")
		}
		for i, char := range label {
			alphanumeric := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
			if !alphanumeric && !(char == '-' && i > 0 && i < len(label)-1) {
				return fmt.Errorf("host label %q may only contain letters, digits and inner hyphens", label)
			}
		}
	}
	return nil
}

// ParsePath validates the script path on the vendor origin. It is concatenated
// into a script URL, so it must not escape the origin or carry a quote.
func ParsePath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if len(value) > 200 {
		return "", errors.New("script path must be 200 characters or fewer")
	}
	if strings.ContainsAny(value, " \t\r\n\"'`<>\\") || strings.Contains(value, "//") || strings.Contains(value, "..") {
		return "", errors.New("script path contains a character that is not allowed")
	}
	for _, char := range value {
		ok := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			strings.ContainsRune("/._-~=?&", char)
		if !ok {
			return "", fmt.Errorf("script path may not contain %q", char)
		}
	}
	return value, nil
}

// ParseSiteID validates a vendor identifier. These reach generated JavaScript as
// a string literal, so only characters that cannot terminate one are allowed.
func ParseSiteID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > 80 {
		return "", errors.New("site id must be 80 characters or fewer")
	}
	for _, char := range value {
		ok := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			strings.ContainsRune("-_.", char)
		if !ok {
			return "", errors.New("site id may only contain letters, digits, hyphen, underscore and dot")
		}
	}
	return value, nil
}

// ParseAttributeName and ParseAttributeValue guard the data-* attributes some
// vendors are configured through.
func ParseAttributeName(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "", errors.New("attribute name is required")
	}
	if !strings.HasPrefix(value, "data-") {
		return "", errors.New("only data-* attributes may be set")
	}
	if len(value) > 60 {
		return "", errors.New("attribute name must be 60 characters or fewer")
	}
	for _, char := range strings.TrimPrefix(value, "data-") {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
			return "", errors.New("attribute name may only contain lowercase letters, digits and hyphens")
		}
	}
	return value, nil
}

func ParseAttributeValue(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) > 200 {
		return "", errors.New("attribute value must be 200 characters or fewer")
	}
	if strings.ContainsAny(value, "\r\n\"'`<>\\") {
		return "", errors.New("attribute value contains a character that is not allowed")
	}
	return value, nil
}

// OriginOf reduces a URL to its origin. Browser violation reports carry the full
// blocked URL, so without this the admin screen could never offer to allow the
// host with one click.
func OriginOf(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	origin, err := ParseOrigin(parsed.Scheme + "://" + parsed.Host)
	if err != nil {
		return "", false
	}
	return origin, true
}
