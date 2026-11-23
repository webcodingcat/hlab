package storageagent

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// State is what the storage VM holds before an apply: whether the data
// disk carries a filesystem, and the box users and box subvolumes found.
type State struct {
	Filesystem bool
	Boxes      []string // box users in /etc/passwd
	Subvolumes []string // box subvolumes below the root
}

// StepKind is the kind of a Step.
type StepKind int

// The kinds of steps an apply runs. StepFormat comes first; StepUpdateBox
// brings the accounts of a box present in the document to its state
// without touching its data.
const (
	StepFormat StepKind = iota
	StepRemoveBox
	StepCreateBox
	StepUpdateBox
)

// Step is one step of an apply; Box is the box user, empty for StepFormat.
type Step struct {
	Kind StepKind
	Box  string
}

// Plan returns the steps that bring have to want: the format step only
// when the disk holds no filesystem, a removal for each box found that the
// document lacks, a creation for each box of the document not fully found,
// and an update for every box of the document. A box present in the
// document is never removed.
func Plan(have State, want agentapi.Storage) []Step {
	var steps []Step
	if !have.Filesystem {
		steps = append(steps, Step{Kind: StepFormat})
	}
	wanted := map[string]bool{}
	for _, b := range want.Boxes {
		wanted[b.User] = true
	}
	var found []string
	for _, u := range append(slices.Clone(have.Boxes), have.Subvolumes...) {
		if !slices.Contains(found, u) {
			found = append(found, u)
		}
	}
	slices.Sort(found)
	for _, u := range found {
		if !wanted[u] {
			steps = append(steps, Step{Kind: StepRemoveBox, Box: u})
		}
	}
	for _, b := range want.Boxes {
		if !have.Filesystem || !slices.Contains(have.Boxes, b.User) || !slices.Contains(have.Subvolumes, b.User) {
			steps = append(steps, Step{Kind: StepCreateBox, Box: b.User})
		}
	}
	for _, b := range want.Boxes {
		steps = append(steps, Step{Kind: StepUpdateBox, Box: b.User})
	}
	return steps
}

var (
	boxUserRE = regexp.MustCompile(`^u[1-9][0-9]{0,8}$`)
	subUserRE = regexp.MustCompile(`^(u[1-9][0-9]{0,8})-sub[1-9][0-9]{0,5}$`)
)

// boxUID returns the UID of box user, the number after the "u".
func boxUID(user string) (int, error) {
	if !boxUserRE.MatchString(user) {
		return 0, fmt.Errorf("invalid box user %q", user)
	}
	return strconv.Atoi(user[1:])
}

// validate checks the names and paths of doc before they reach commands
// and paths.
func validate(doc agentapi.Storage) error {
	seen := map[string]bool{}
	for _, b := range doc.Boxes {
		if _, err := boxUID(b.User); err != nil {
			return err
		}
		if b.QuotaBytes <= 0 {
			return fmt.Errorf("box %s: invalid quota %d", b.User, b.QuotaBytes)
		}
		if seen[b.User] {
			return fmt.Errorf("box %s: listed twice", b.User)
		}
		seen[b.User] = true
		for _, s := range b.Subaccounts {
			m := subUserRE.FindStringSubmatch(s.User)
			if m == nil || m[1] != b.User {
				return fmt.Errorf("box %s: invalid subaccount user %q", b.User, s.User)
			}
			if seen[s.User] {
				return fmt.Errorf("subaccount %s: listed twice", s.User)
			}
			seen[s.User] = true
		}
		for _, h := range append([]agentapi.Hashes{b.Hashes}, subHashes(b)...) {
			if strings.ContainsAny(h.Crypt, ":\r\n") {
				return fmt.Errorf("box %s: invalid crypt hash", b.User)
			}
		}
		for _, k := range b.SSHKeys {
			if strings.ContainsAny(k, "\r\n") {
				return fmt.Errorf("box %s: SSH key spans lines", b.User)
			}
		}
	}
	return nil
}

// subHashes returns the hashes of the subaccounts of b.
func subHashes(b agentapi.Box) []agentapi.Hashes {
	var out []agentapi.Hashes
	for _, s := range b.Subaccounts {
		out = append(out, s.Hashes)
	}
	return out
}
