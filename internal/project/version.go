package project

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var comparator = regexp.MustCompile(`^(>=|<=|>|<|==|=|\^|~|~=)?v?([0-9]+)(?:\.([0-9]+|x|\*))?(?:\.([0-9]+|x|\*))?$`)
var comparatorSpacing = regexp.MustCompile(`(>=|<=|==|~=|\^|~|>|<|=)\s+`)

// MatchesRuntime intentionally supports the common declarative numeric
// constraints only. Unknown expressions fail explicitly instead of guessing.
func MatchesRuntime(actual, requirement string) (bool, error) {
	version, err := versionParts(actual)
	if err != nil {
		return false, err
	}
	requirement = comparatorSpacing.ReplaceAllString(requirement, "$1")
	for _, alternative := range strings.Split(requirement, "||") {
		tokens := strings.Fields(strings.ReplaceAll(alternative, ",", " "))
		if len(tokens) == 0 {
			return false, fmt.Errorf("empty runtime constraint")
		}
		match := true
		for _, token := range tokens {
			if token == "*" {
				continue
			}
			m := comparator.FindStringSubmatch(token)
			if m == nil {
				return false, fmt.Errorf("unsupported runtime constraint %q; supported forms use numeric comparisons, ^, ~ and ||", requirement)
			}
			target := [3]int{}
			count := 0
			for i := 0; i < 3; i++ {
				if m[i+2] == "" || m[i+2] == "x" || m[i+2] == "*" {
					break
				}
				var err error
				target[i], err = strconv.Atoi(m[i+2])
				if err != nil {
					return false, fmt.Errorf("runtime constraint version is too large: %q", token)
				}
				count++
			}
			cmp := compareVersion(version, target)
			switch m[1] {
			case ">=":
				match = match && cmp >= 0
			case ">":
				match = match && cmp > 0
			case "<=":
				match = match && cmp <= 0
			case "<":
				match = match && cmp < 0
			case "^":
				upper := [3]int{target[0] + 1, 0, 0}
				if target[0] == 0 && count >= 2 {
					upper = [3]int{0, target[1] + 1, 0}
					if target[1] == 0 && count == 3 {
						upper = [3]int{0, 0, target[2] + 1}
					}
				}
				match = match && cmp >= 0 && compareVersion(version, upper) < 0
			case "~":
				match = match && cmp >= 0 && version[0] == target[0] && (count < 2 || version[1] == target[1])
			case "~=":
				match = match && cmp >= 0 && version[0] == target[0] && (count < 3 || version[1] == target[1])
			default:
				for i := 0; i < count; i++ {
					match = match && version[i] == target[i]
				}
			}
		}
		if match {
			return true, nil
		}
	}
	return false, nil
}

func versionParts(s string) ([3]int, error) {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("invalid runtime version %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("invalid runtime version %q", s)
		}
		v[i] = n
	}
	return v, nil
}
func compareVersion(a, b [3]int) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
