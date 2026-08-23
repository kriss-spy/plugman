package source

import (
	"fmt"
	"strconv"
	"strings"
)

// semver is deliberately private: callers deal in release-domain values, not
// comparison mechanics. It implements precedence from Semantic Versioning 2.0.
type semver struct {
	original   string
	major      uint64
	minor      uint64
	patch      uint64
	prerelease []semverIdentifier
}

type semverIdentifier struct {
	text    string
	numeric bool
	number  uint64
}

func parseSemver(value string) (semver, error) {
	result := semver{original: value}
	if value == "" || strings.TrimSpace(value) != value {
		return semver{}, fmt.Errorf("invalid semantic version %q", value)
	}

	precedence := value
	if plus := strings.IndexByte(precedence, '+'); plus >= 0 {
		if err := validateIdentifiers(precedence[plus+1:], false); err != nil {
			return semver{}, fmt.Errorf("invalid build metadata in %q: %w", value, err)
		}
		precedence = precedence[:plus]
	}
	if dash := strings.IndexByte(precedence, '-'); dash >= 0 {
		identifiers, err := parsePrerelease(precedence[dash+1:])
		if err != nil {
			return semver{}, fmt.Errorf("invalid prerelease in %q: %w", value, err)
		}
		result.prerelease = identifiers
		precedence = precedence[:dash]
	}

	parts := strings.Split(precedence, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("semantic version %q must have major.minor.patch", value)
	}
	components := []*uint64{&result.major, &result.minor, &result.patch}
	for i, part := range parts {
		number, err := parseNumericIdentifier(part)
		if err != nil {
			return semver{}, fmt.Errorf("invalid semantic version %q: %w", value, err)
		}
		*components[i] = number
	}
	return result, nil
}

func parsePrerelease(value string) ([]semverIdentifier, error) {
	if err := validateIdentifiers(value, true); err != nil {
		return nil, err
	}
	parts := strings.Split(value, ".")
	identifiers := make([]semverIdentifier, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.ParseUint(part, 10, 64)
		if err == nil {
			if len(part) > 1 && part[0] == '0' {
				return nil, fmt.Errorf("numeric identifier %q has a leading zero", part)
			}
			identifiers = append(identifiers, semverIdentifier{text: part, numeric: true, number: number})
			continue
		}
		identifiers = append(identifiers, semverIdentifier{text: part})
	}
	return identifiers, nil
}

func validateIdentifiers(value string, prerelease bool) error {
	if value == "" {
		return fmt.Errorf("identifier list is empty")
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return fmt.Errorf("identifier is empty")
		}
		for _, character := range identifier {
			if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && character != '-' {
				return fmt.Errorf("identifier %q contains an invalid character", identifier)
			}
		}
		if prerelease && allDigits(identifier) && len(identifier) > 1 && identifier[0] == '0' {
			return fmt.Errorf("numeric identifier %q has a leading zero", identifier)
		}
	}
	return nil
}

func parseNumericIdentifier(value string) (uint64, error) {
	if value == "" || !allDigits(value) {
		return 0, fmt.Errorf("numeric identifier %q is invalid", value)
	}
	if len(value) > 1 && value[0] == '0' {
		return 0, fmt.Errorf("numeric identifier %q has a leading zero", value)
	}
	result, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("numeric identifier %q is too large", value)
	}
	return result, nil
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

func (v semver) hasPrerelease() bool { return len(v.prerelease) != 0 }

func (v semver) compare(other semver) int {
	for _, pair := range [][2]uint64{{v.major, other.major}, {v.minor, other.minor}, {v.patch, other.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(v.prerelease) == 0 && len(other.prerelease) == 0 {
		return 0
	}
	if len(v.prerelease) == 0 {
		return 1
	}
	if len(other.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(v.prerelease) && i < len(other.prerelease); i++ {
		left, right := v.prerelease[i], other.prerelease[i]
		if left.numeric && right.numeric {
			if left.number < right.number {
				return -1
			}
			if left.number > right.number {
				return 1
			}
			continue
		}
		if left.numeric != right.numeric {
			if left.numeric {
				return -1
			}
			return 1
		}
		if left.text < right.text {
			return -1
		}
		if left.text > right.text {
			return 1
		}
	}
	if len(v.prerelease) < len(other.prerelease) {
		return -1
	}
	if len(v.prerelease) > len(other.prerelease) {
		return 1
	}
	return 0
}

// CompareVersions compares two exact semantic versions.
func CompareVersions(left, right string) (int, error) {
	a, err := parseSemver(left)
	if err != nil {
		return 0, err
	}
	b, err := parseSemver(right)
	if err != nil {
		return 0, err
	}
	return a.compare(b), nil
}
