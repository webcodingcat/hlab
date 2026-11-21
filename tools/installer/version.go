package main

import (
	"fmt"
	"regexp"
	"strconv"
)

var versionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// validateVersion accepts only x.y.z within the limits of an MSI
// ProductVersion: major and minor at most 255, patch at most 65535.
func validateVersion(v string) error {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return fmt.Errorf("version %q is not of the form x.y.z (for example task installer VERSION=1.2.3)", v)
	}
	limits := []int{255, 255, 65535}
	names := []string{"major", "minor", "patch"}
	for i, limit := range limits {
		n, err := strconv.Atoi(m[i+1])
		if err != nil || n > limit {
			return fmt.Errorf("version %q: %s must be at most %d", v, names[i], limit)
		}
	}
	return nil
}
