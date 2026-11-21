// Command installer builds the release installers of hlab: the four
// binaries with profile release into <out>\release\, the MSI
// <out>\hlab-<version>.msi from installer\Package.wxs and the setup bundle
// <out>\hlab-setup-<version>.exe from installer\Bundle.wxs, which embeds the
// cached WebView2 Evergreen bootstrapper. Run it from the repository root
// (`task installer VERSION=x.y.z`).
//
// Usage: installer [-out dir] <version>
//
// Bind paths: the MSI is built with <out>\release (the binaries) and the
// repository root (catalog\*.json); the bundle with <out> (the MSI) and the
// build cache (MicrosoftEdgeWebview2Setup.exe). Both get -d Version=<version>.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	wixVersion     = "7.0.1"
	webview2URL    = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
	webview2File   = "MicrosoftEdgeWebview2Setup.exe"
	webview2MaxAge = 7 * 24 * time.Hour
	cacheFolder    = "hlab-build-cache" // under %LocalAppData%, outside %LocalAppData%\hlab which uninstall deletes
	utilExt        = "WixToolset.Util.wixext"
	balExt         = "WixToolset.BootstrapperApplications.wixext"
)

func main() {
	out := flag.String("out", "bin", "output folder for the installers; the binaries go to <out>\\release")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: installer [-out dir] <version>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *out); err != nil {
		fmt.Fprintln(os.Stderr, "installer:", err)
		os.Exit(1)
	}
}

func run(version, out string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if _, err := exec.LookPath("wix"); err != nil {
		return fmt.Errorf("wix not found on PATH; install it with: dotnet tool install --global wix --version %s", wixVersion)
	}
	if err := checkExtensions(); err != nil {
		return err
	}
	out, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return errors.New("run from the repository root")
	}
	binDir := filepath.Join(out, "release")
	if err := buildBinaries(version, binDir); err != nil {
		return err
	}
	cacheDir, err := fetchWebView2()
	if err != nil {
		return err
	}

	msi := filepath.Join(out, "hlab-"+version+".msi")
	if err := wixBuild(version, []string{utilExt}, []string{binDir, root}, msi,
		filepath.Join(root, "installer", "Package.wxs")); err != nil {
		return err
	}
	setup := filepath.Join(out, "hlab-setup-"+version+".exe")
	if err := wixBuild(version, []string{balExt, utilExt}, []string{out, cacheDir}, setup,
		filepath.Join(root, "installer", "Bundle.wxs")); err != nil {
		return err
	}
	fmt.Println("built", msi)
	fmt.Println("built", setup)
	return nil
}

// checkExtensions looks for both extensions in the current user's global
// WiX extension cache (%USERPROFILE%\.wix\extensions\<id>\<version>).
func checkExtensions() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var missing []error
	for _, id := range []string{utilExt, balExt} {
		if _, err := os.Stat(filepath.Join(home, ".wix", "extensions", id, wixVersion)); err != nil {
			missing = append(missing, fmt.Errorf("WiX extension %s %s is not in the global cache; add it with: wix extension add -g %s/%s",
				id, wixVersion, id, wixVersion))
		}
	}
	return errors.Join(missing...)
}

func buildBinaries(version, binDir string) error {
	ldflags := "-X github.com/webcodingcat/hlab/internal/version.Version=" + version +
		" -X github.com/webcodingcat/hlab/internal/profile.Name=release"
	builds := []struct {
		out, pkg, extra string
		linux           bool
	}{
		{"hlabd.exe", "./cmd/hlabd", "", false},
		{"hlab.exe", "./cmd/hlab", "", false},
		{"hlab-tray.exe", "./cmd/hlab-tray", " -H windowsgui", false},
		{"hlab-agent", "./cmd/hlab-agent", "", true},
	}
	for _, b := range builds {
		cmd := exec.Command("go", "build", "-ldflags", ldflags+b.extra, "-o", filepath.Join(binDir, b.out), b.pkg)
		cmd.Env = os.Environ()
		if b.linux {
			cmd.Env = append(cmd.Env, "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		}
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build %s: %w", b.pkg, err)
		}
	}
	return nil
}

// fetchWebView2 keeps the bootstrapper in %LocalAppData%\hlab-build-cache
// and downloads it when it is missing or older than seven days. A failed
// download with a cached copy uses the copy. It returns the cache folder.
func fetchWebView2() (string, error) {
	local := os.Getenv("LocalAppData")
	if local == "" {
		return "", errors.New("LocalAppData is not set")
	}
	dir := filepath.Join(local, cacheFolder)
	dest := filepath.Join(dir, webview2File)
	info, statErr := os.Stat(dest)
	if statErr == nil && time.Since(info.ModTime()) < webview2MaxAge {
		return dir, nil
	}
	if err := download(dir, dest); err != nil {
		if statErr == nil {
			fmt.Fprintf(os.Stderr, "installer: download of %s failed (%v); using the cached copy from %s\n",
				webview2File, err, info.ModTime().Format(time.DateOnly))
			return dir, nil
		}
		return "", fmt.Errorf("download %s: %w", webview2URL, err)
	}
	return dir, nil
}

func download(dir, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webview2URL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, webview2File+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

func wixBuild(version string, exts, bindPaths []string, out, source string) error {
	args := []string{"build", "-acceptEula", "wix7", "-arch", "x64", "-d", "Version=" + version}
	for _, e := range exts {
		args = append(args, "-ext", e)
	}
	for _, b := range bindPaths {
		args = append(args, "-b", b)
	}
	args = append(args, "-o", out, source)
	cmd := exec.Command("wix", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wix build %s: %w", filepath.Base(source), err)
	}
	return nil
}
