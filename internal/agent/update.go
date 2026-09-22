package agent

// Updating a fleet by hand is how fleets end up with five versions. The server already hands
// out the agent it was built with, so an agent that is allowed to can keep itself aligned:
// nothing comes from the internet, the binary comes from the same server the measures go to,
// its checksum is checked and it has to run and say its version before it is put in place.
// Off by default: replacing a binary on a production machine is the team's decision.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// AgentPlatform is what this build asks the server for.
func AgentPlatform() string { return runtime.GOOS + "-" + runtime.GOARCH }

type fetcher func(ctx context.Context, path string) ([]byte, error)

// replaceBinary downloads the agent the server carries and puts it in place of exe. Every step
// that fails leaves the running agent exactly where it is.
func replaceBinary(ctx context.Context, fetch fetcher, download, version, exe string) error {
	binary, err := fetch(ctx, download)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	raw, err := fetch(ctx, download+".sha256")
	if err != nil {
		return fmt.Errorf("checksum: %w", err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return fmt.Errorf("checksum: empty answer")
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(binary)); got != fields[0] {
		return fmt.Errorf("checksum mismatch: the server says %s, the download is %s", fields[0], got)
	}

	// Next to the binary, so the rename is atomic and stays on the same filesystem.
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return err
	}
	defer os.Remove(tmp)

	// It has to run here before it runs as the agent: wrong architecture, truncated download,
	// anything at all shows up now instead of in a restart loop.
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(check, tmp, "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the downloaded agent does not run: %w (%s)", err, bytes.TrimSpace(out))
	}
	if !strings.Contains(string(out), version) {
		return fmt.Errorf("the downloaded agent says %q, the server says %q", strings.TrimSpace(string(out)), version)
	}
	return os.Rename(tmp, exe)
}

// inContainer answers for images, where the binary is not the truth: replacing it would work
// until the next restart and then quietly come back. There the image is what gets updated.
func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if bytes.Contains(b, []byte("docker")) || bytes.Contains(b, []byte("containerd")) || bytes.Contains(b, []byte("kubepods")) {
			return true
		}
	}
	return false
}

// upgrade decides what to do when the server turns out to be a different version. It returns
// true when this process has to stop so the supervisor can start the new binary.
func (r *Runner) upgrade(ctx context.Context, serverVersion, download string) bool {
	if serverVersion == "" || serverVersion == Version || r.saidAbout == serverVersion {
		return false
	}
	switch {
	case !r.cfg.AutoUpdate:
		r.saidAbout = serverVersion
		slog.Info("a newer agent is waiting on the server", "server", serverVersion, "agent", Version,
			"how", "re-run the install command, or set auto_update: true in agent.yaml")
		return false
	case inContainer():
		r.saidAbout = serverVersion
		slog.Info("a newer agent is waiting on the server: update the image, the binary inside it is not the truth",
			"server", serverVersion, "agent", Version)
		return false
	case download == "":
		r.saidAbout = serverVersion
		slog.Warn("the server is newer but does not carry the agent for this machine",
			"server", serverVersion, "agent", Version, "platform", AgentPlatform())
		return false
	}

	exe, err := os.Executable()
	if err != nil {
		slog.Warn("update: cannot find my own binary", "error", err)
		return false
	}
	if err := replaceBinary(ctx, r.sender.Fetch, download, serverVersion, exe); err != nil {
		r.saidAbout = serverVersion // do not try again every round
		slog.Warn("update failed, going on with the version I have", "error", err, "agent", Version)
		return false
	}
	slog.Info("updated, restarting", "from", Version, "to", serverVersion, "binary", exe)
	return true
}
