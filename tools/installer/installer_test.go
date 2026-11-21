package main

import "testing"

func TestInstaller(t *testing.T) {
	t.Run("INST-06", func(t *testing.T) {
		cases := []struct {
			version string
			ok      bool
		}{
			{"0.0.1", true},
			{"255.255.65535", true},
			{"1.2", false},
			{"1.2.3.4", false},
			{"v1.2.3", false},
			{"256.0.0", false},
			{"1.0.65536", false},
			{"dev", false},
		}
		for _, c := range cases {
			t.Run(c.version, func(t *testing.T) {
				err := validateVersion(c.version)
				if c.ok && err != nil {
					t.Errorf("validateVersion(%q) = %v, want accepted", c.version, err)
				}
				if !c.ok && err == nil {
					t.Errorf("validateVersion(%q) accepted, want refused", c.version)
				}
			})
		}
	})
}
