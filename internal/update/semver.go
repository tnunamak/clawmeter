package update

import (
	"fmt"
	"regexp"
	"strings"
)

var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func parseVersion(tag string) ([]string, error) {
	parts := semverPattern.FindStringSubmatch(strings.TrimPrefix(tag, "v"))
	if parts == nil {
		return nil, fmt.Errorf("invalid semver %q", tag)
	}
	if parts[4] != "" {
		for _, id := range strings.Split(parts[4], ".") {
			if numericIdentifier(id) && len(id) > 1 && id[0] == '0' {
				return nil, fmt.Errorf("invalid semver %q", tag)
			}
		}
	}
	return parts, nil
}

func numericIdentifier(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// Compare decimal strings without an integer-size limit.
func compareNumeric(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func newerVersion(current, next string) (bool, error) {
	a, err := parseVersion(current)
	if err != nil {
		return false, err
	}
	b, err := parseVersion(next)
	if err != nil {
		return false, err
	}
	for i := 1; i <= 3; i++ {
		if c := compareNumeric(b[i], a[i]); c != 0 {
			return c > 0, nil
		}
	}
	if a[4] == "" || b[4] == "" {
		return a[4] != "" && b[4] == "", nil
	}
	ap, bp := strings.Split(a[4], "."), strings.Split(b[4], ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		an, bn := numericIdentifier(ap[i]), numericIdentifier(bp[i])
		c := strings.Compare(bp[i], ap[i])
		switch {
		case an && bn:
			c = compareNumeric(bp[i], ap[i])
		case an && !bn:
			c = 1
		case !an && bn:
			c = -1
		}
		if c != 0 {
			return c > 0, nil
		}
	}
	return len(bp) > len(ap), nil
}
