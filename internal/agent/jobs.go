package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// jobFunc returns the function that runs job, or an error when the kind or
// its parameters are not known.
func jobFunc(job agentapi.Job) (func(context.Context) error, error) {
	if job.LUN < 0 {
		return nil, fmt.Errorf("invalid LUN %d", job.LUN)
	}
	switch job.Kind {
	case agentapi.JobMkfs:
		if job.FS != "ext4" && job.FS != "xfs" {
			return nil, fmt.Errorf("mkfs: unsupported filesystem %q, want ext4 or xfs", job.FS)
		}
		return func(ctx context.Context) error { return mkfs(ctx, job.LUN, job.FS) }, nil
	case agentapi.JobRescuePrepare:
		return func(ctx context.Context) error { return rescuePrepare(ctx, job.LUN) }, nil
	default:
		return nil, fmt.Errorf("unknown job kind %q", job.Kind)
	}
}

// RewriteIDs replaces every occurrence of each old identifier (map key) by
// its new one (map value) in text; nothing else changes.
func RewriteIDs(text string, ids map[string]string) string {
	olds := make([]string, 0, len(ids))
	for old := range ids {
		if old != "" {
			olds = append(olds, old)
		}
	}
	// Longest first, so an identifier that contains another one wins.
	sort.Slice(olds, func(i, j int) bool {
		if len(olds[i]) != len(olds[j]) {
			return len(olds[i]) > len(olds[j])
		}
		return olds[i] < olds[j]
	})
	pairs := make([]string, 0, 2*len(olds))
	for _, old := range olds {
		pairs = append(pairs, old, ids[old])
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
