package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/sshkey"
)

// labKeyFile is the lab's private SSH key in the data folder, which every
// server's root takes, for `hlab ssh`. It stays across restarts, so that
// the servers keep taking it; its comment names it in authorized_keys.
const (
	labKeyFile    = "lab-key"
	labKeyComment = "hlab"
)

// loadLabKey returns the lab's private SSH key and its public key, and
// makes it the first time. It is written like the control token, readable
// for the local users: they may read the control token, and with it ask
// the lab control for this key anyway.
func loadLabKey(dataDir string, lifecycle platform.Lifecycle) (private []byte, public string, err error) {
	path := filepath.Join(dataDir, labKeyFile)
	private, err = os.ReadFile(path)
	if err == nil {
		public, err = sshkey.Public(private, labKeyComment)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", path, err)
		}
		return private, public, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	private, public, err = sshkey.New(labKeyComment)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, "", err
	}
	if err := lifecycle.WriteSecret(path, private); err != nil {
		return nil, "", fmt.Errorf("writing the lab's SSH key: %w", err)
	}
	return private, public, nil
}
