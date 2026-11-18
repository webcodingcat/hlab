//go:build windows

package cloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// applianceJobLUN is where the disk of an appliance job is hot-added; LUN 0
// is the appliance's own disk.
const applianceJobLUN = 1

// applianceJob runs job on the appliance with diskPath hot-added at LUN 1,
// one job at a time. The disk is detached again on every path.
func (c *Cloud) applianceJob(ctx context.Context, diskPath string, job agentapi.Job) (err error) {
	c.jobMu.Lock()
	defer c.jobMu.Unlock()

	name := c.applianceName()
	if err := c.plat.AttachDisk(ctx, name, diskPath, applianceJobLUN); err != nil {
		return fmt.Errorf("attach %s to the appliance: %w", diskPath, err)
	}
	defer func() {
		if derr := c.plat.DetachDisk(context.WithoutCancel(ctx), name, diskPath); derr != nil {
			err = errors.Join(err, fmt.Errorf("detach %s from the appliance: %w", diskPath, derr))
		}
	}()
	job.LUN = applianceJobLUN
	slog.Info("appliance job", "kind", job.Kind, "disk", diskPath)
	_, err = c.agent.Job(ctx, job)
	return err
}

// prepareRescue writes the rescue base dst: a copy of base made unique by
// the agent job rescue-prepare, renamed into place when the job succeeded.
func (c *Cloud) prepareRescue(ctx context.Context, base, dst string) error {
	tmp := dst + ".partial.vhdx"
	// A partial copy is a leftover of an earlier failed preparation.
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := c.plat.CreateDisk(ctx, tmp, 0, base, [16]byte{}); err != nil {
		return err
	}
	err := c.applianceJob(ctx, tmp, agentapi.Job{Kind: agentapi.JobRescuePrepare})
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
