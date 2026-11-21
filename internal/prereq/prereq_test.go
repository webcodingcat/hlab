package prereq_test

import (
	"regexp"
	"testing"

	"github.com/webcodingcat/hlab/internal/prereq"
)

// Each missing prerequisite has its own line; a line is recognised by the
// words it must contain, its position gives the fixed order.
var (
	lineArch     = regexp.MustCompile(`(?i)x64|64-bit`)
	lineWindows  = regexp.MustCompile(`(?i)22000|20348|windows 11|server 2022`)
	lineEdition  = regexp.MustCompile(`(?i)home|edition`)
	lineVirt     = regexp.MustCompile(`(?i)virtuali[sz]ation`)
	lineHyperV   = regexp.MustCompile(`(?i)hyper-v`)
	linePSModule = regexp.MustCompile(`(?i)powershell`)
	lineRestart  = regexp.MustCompile(`^Restart Windows to finish enabling Hyper-V$`)
)

func complete() prereq.Facts {
	return prereq.Facts{
		Arch:         "amd64",
		Build:        26100,
		EditionID:    "Professional",
		VMMS:         true,
		PSModule:     true,
		Hypervisor:   true,
		FirmwareVirt: true,
	}
}

func TestPrereq(t *testing.T) {
	t.Run("INST-02", func(t *testing.T) {
		cases := []struct {
			name  string
			facts func(f *prereq.Facts)
			want  []*regexp.Regexp
		}{
			{"all present", func(f *prereq.Facts) {}, nil},
			{"arm64", func(f *prereq.Facts) { f.Arch = "arm64" }, []*regexp.Regexp{lineArch}},
			{"build 19045", func(f *prereq.Facts) { f.Build = 19045 }, []*regexp.Regexp{lineWindows}},
			{"server build 17763", func(f *prereq.Facts) {
				f.Server, f.Build, f.EditionID = true, 17763, "ServerStandard"
			}, []*regexp.Regexp{lineWindows}},
			{"home Core", func(f *prereq.Facts) { f.EditionID = "Core" }, []*regexp.Regexp{lineEdition}},
			{"home CoreN", func(f *prereq.Facts) { f.EditionID = "CoreN" }, []*regexp.Regexp{lineEdition}},
			{"home CoreSingleLanguage", func(f *prereq.Facts) { f.EditionID = "CoreSingleLanguage" }, []*regexp.Regexp{lineEdition}},
			{"home CoreCountrySpecific", func(f *prereq.Facts) { f.EditionID = "CoreCountrySpecific" }, []*regexp.Regexp{lineEdition}},
			{"no firmware virtualization and no hypervisor", func(f *prereq.Facts) {
				f.FirmwareVirt, f.Hypervisor = false, false
			}, []*regexp.Regexp{lineVirt}},
			{"hypervisor present with firmware flag false", func(f *prereq.Facts) { f.FirmwareVirt = false }, nil},
			{"no vmms", func(f *prereq.Facts) { f.VMMS = false }, []*regexp.Regexp{lineHyperV}},
			{"no powershell module", func(f *prereq.Facts) { f.PSModule = false }, []*regexp.Regexp{linePSModule}},
			{"restart pending", func(f *prereq.Facts) { f.Hypervisor = false }, []*regexp.Regexp{lineRestart}},
			{"several missing", func(f *prereq.Facts) {
				*f = prereq.Facts{Arch: "arm64", Build: 19045, EditionID: "Core"}
			}, []*regexp.Regexp{lineArch, lineWindows, lineEdition, lineVirt, lineHyperV, linePSModule}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				f := complete()
				c.facts(&f)
				got := prereq.Missing(f)
				if len(got) != len(c.want) {
					t.Fatalf("Missing(%+v) = %q, want %d lines", f, got, len(c.want))
				}
				for i, re := range c.want {
					if !re.MatchString(got[i]) {
						t.Errorf("line %d %q does not match %s", i, got[i], re)
					}
				}
			})
		}
	})
}
