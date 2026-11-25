//go:build windows

package cloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sys/windows"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/agentclient"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
	"github.com/webcodingcat/hlab/internal/store"
)

const (
	// storageReadyTimeout bounds the wait for the storage agent after the
	// storage VM was ensured (first boot: cloud-init, package install).
	storageReadyTimeout = 5 * time.Minute
	// storageRetry is the pause between the start's attempts to bring the
	// storage VM in line with the store.
	storageRetry = 15 * time.Second
	// storageTrimTimeout bounds the trim of the storage VM's data file
	// system before its shutdown.
	storageTrimTimeout = 5 * time.Minute
)

// newStorageClient returns the client of the storage agent.
func (c *Cloud) newStorageClient() (*agentclient.StorageClient, error) {
	secret, err := c.storageSecret()
	if err != nil {
		return nil, err
	}
	return agentclient.NewStorage(agentclient.Options{Addr: c.addrs.StorageAgent.String(), Secret: secret, Store: c.st}), nil
}

// storageDeps are the storage box module's hooks into the storage VM.
func (c *Cloud) storageDeps() storageboxes.Deps {
	return storageboxes.Deps{
		Catalog:     c.cat,
		PublicRange: c.set.Network.PublicRange,
		Store:       c.st,
		Apply:       func(ctx context.Context, _ int64) error { return c.applyStorage(ctx) },
		// A job waits for a running apply: a document built before a
		// snapshot record was stored must reach the agent before the
		// snapshot exists, or the agent removes it as unlisted.
		Job: func(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error) {
			c.storageMu.Lock()
			defer c.storageMu.Unlock()
			return c.storage.Job(ctx, job)
		},
		FreeBytes: c.freeBytes,
		PushAgent: c.PushAgent,
		EndFaults: c.endFaults,
	}
}

// applyStorage makes the storage VM match the store. Without boxes it
// deletes the VM and the folder storage\; otherwise it pushes the agent
// document first (the appliance routes the storage address and lets the
// storage MAC through only with it), ensures the VM, waits for the storage
// agent and pushes the storage document. It returns after the agent
// applied it.
func (c *Cloud) applyStorage(ctx context.Context) error {
	c.storageMu.Lock()
	defer c.storageMu.Unlock()
	var boxes []agentapi.Box
	err := c.st.Tx(func(tx store.Tx) error {
		var err error
		boxes, err = storageboxes.Boxes(tx)
		return err
	})
	if err != nil {
		return fmt.Errorf("build storage document: %w", err)
	}
	if len(boxes) == 0 {
		if err := c.deleteStorageVM(ctx); err != nil {
			return err
		}
		return c.PushAgent(ctx)
	}
	if err := c.PushAgent(ctx); err != nil {
		return err
	}
	if err := c.ensureStorageVM(ctx); err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, storageReadyTimeout)
	err = c.storage.WaitReady(rctx)
	cancel()
	if err != nil {
		return err
	}
	cert, err := c.storageTLS()
	if err != nil {
		return err
	}
	doc := agentapi.Storage{
		PublicRange: c.set.Network.PublicRange,
		HostNet:     c.addrs.Host.Masked(),
		TLS:         cert,
		Boxes:       boxes,
	}
	if err := c.storage.Push(ctx, doc); err != nil {
		slog.Error("storage document push failed", "boxes", len(boxes), "err", err)
		return err
	}
	slog.Info("storage document pushed", "boxes", len(boxes))
	return nil
}

// freeBytes returns the free bytes of the host drive of the data folder.
func (c *Cloud) freeBytes() (uint64, error) {
	dir, err := windows.UTF16PtrFromString(c.set.Storage.DataFolder)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(dir, &avail, &total, &free); err != nil {
		return 0, fmt.Errorf("free space of %s: %w", c.set.Storage.DataFolder, err)
	}
	return free, nil
}

// startStorage brings the storage VM in line with the store in the
// background when boxes exist or the folder storage\ is left over, retrying
// until it succeeds or the cloud stops.
func (c *Cloud) startStorage(ctx context.Context) {
	var boxes []agentapi.Box
	err := c.st.Tx(func(tx store.Tx) error {
		var err error
		boxes, err = storageboxes.Boxes(tx)
		return err
	})
	if err != nil {
		slog.Error("storage VM at start", "err", err)
		return
	}
	if _, serr := os.Stat(c.storageDir()); len(boxes) == 0 && serr != nil {
		return
	}
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			err := c.applyStorage(sctx)
			if err == nil {
				return
			}
			if sctx.Err() != nil {
				return
			}
			slog.Warn("storage VM at start, retrying", "err", err, "retry_in", storageRetry.String())
			select {
			case <-sctx.Done():
				return
			case <-time.After(storageRetry):
			}
		}
	}()
	c.stopStorage = func() {
		cancel()
		<-done
	}
}

// haltStorage stops the storage VM per behaviour ("save" or "shutdown")
// before the appliance stops. A VM that is off afterwards gets its data
// disk compacted; a failure is only logged. At a Windows shutdown Hyper-V
// has already applied the VM's stop action, so nothing is done.
func (c *Cloud) haltStorage(ctx context.Context, behaviour string, hostShutdown bool) error {
	if hostShutdown {
		return nil
	}
	name := c.storageName()
	state, err := c.plat.State(ctx, name)
	var perr *platform.Error
	if errors.As(err, &perr) && perr.Kind == platform.NotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage VM state: %w", err)
	}
	if state == platform.StateRunning {
		if behaviour == "shutdown" {
			c.trimStorage(ctx)
			err = c.shutdownVM(ctx, name)
		} else if err = c.plat.Power(ctx, name, platform.PowerSave); err != nil {
			slog.Warn("save of the storage VM failed, shutting it down", "err", err)
			c.trimStorage(ctx)
			err = c.shutdownVM(ctx, name)
		}
		if err != nil {
			return fmt.Errorf("stop the storage VM: %w", err)
		}
		if state, err = c.plat.State(ctx, name); err != nil {
			return fmt.Errorf("storage VM state: %w", err)
		}
	}
	if state != platform.StateOff {
		slog.Info("data disk of the storage VM not compacted, the VM is not off", "state", state.String(), "stop_behaviour", behaviour)
		return nil
	}
	start := time.Now()
	if err := c.plat.CompactDisk(ctx, c.storageDataDisk()); err != nil {
		slog.Error("compact the data disk of the storage VM", "path", c.storageDataDisk(), "err", err)
		return nil
	}
	slog.Info("data disk of the storage VM compacted", "path", c.storageDataDisk(), "took", time.Since(start).Round(time.Second).String())
	return nil
}

// trimStorage has the storage agent discard the free blocks of the data
// file system before the VM shuts down, so the compaction gives back the
// space of deleted data. A failure is only logged: the VM still shuts down
// and the compaction reclaims what was discarded.
func (c *Cloud) trimStorage(ctx context.Context) {
	tctx, cancel := context.WithTimeout(ctx, storageTrimTimeout)
	defer cancel()
	start := time.Now()
	c.storageMu.Lock()
	_, err := c.storage.Job(tctx, agentapi.StorageJob{Kind: agentapi.StorageJobTrim})
	c.storageMu.Unlock()
	if err != nil {
		slog.Warn("trim of the storage VM's data file system failed", "err", err)
		return
	}
	slog.Info("free blocks of the storage VM's data file system discarded", "took", time.Since(start).Round(time.Second).String())
}
