package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
	"strings"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// raw is a request that returns bytes: the install script and the agent are not JSON.
func (c *client) raw(path string, headers ...string) (int, []byte, http.Header) {
	c.t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, body, res.Header
}

// One command installs a machine: the server hands out the script and the agent it was built with.
func TestInstallScriptAndAgentDownload(t *testing.T) {
	c := newClient(t)
	c.login()
	token := c.project()["token"].(string)

	dir := t.TempDir()
	binary := fakeAgentSaying("agent")
	if err := os.WriteFile(filepath.Join(dir, "slowpoke-agent-linux-amd64"), binary, 0o755); err != nil {
		t.Fatal(err)
	}
	c.srv.AgentDir = dir

	code, script, header := c.raw("/install.sh")
	if code != 200 || !strings.HasPrefix(header.Get("Content-Type"), "text/x-shellscript") {
		t.Fatalf("install.sh %d %q", code, header.Get("Content-Type"))
	}
	text := string(script)
	if !strings.Contains(text, c.base) {
		t.Fatalf("the script must know its own server:\n%s", text)
	}
	if strings.Contains(text, token) {
		t.Fatal("the script is public: it must never carry a token")
	}
	for _, step := range []string{"detect -server", "systemctl", "/etc/slowpoke/agent.yaml", "sha256", "slowpoke-agent"} {
		if !strings.Contains(text, step) {
			t.Fatalf("the script does not %q", step)
		}
	}
	// It runs on any POSIX shell, not only bash.
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, script, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}

	if code, _, _ := c.raw("/agent/linux-amd64"); code != 401 {
		t.Fatalf("the agent is served to a machine with a token, got %d", code)
	}
	code, got, header := c.raw("/agent/linux-amd64", "Authorization", "Bearer "+token)
	if code != 200 || string(got) != string(binary) {
		t.Fatalf("agent %d %q", code, got)
	}
	if header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("content type %q", header.Get("Content-Type"))
	}
	sum := sha256.Sum256(binary)
	code, got, _ = c.raw("/agent/linux-amd64.sha256", "Authorization", "Bearer "+token)
	if code != 200 || !strings.HasPrefix(string(got), hex.EncodeToString(sum[:])) {
		t.Fatalf("checksum %d %q", code, got)
	}

	if code, _, _ := c.raw("/agent/plan9-mips", "Authorization", "Bearer "+token); code != 404 {
		t.Fatalf("unknown platform: %d", code)
	}
	// A server that was not built with the agents says so instead of serving nothing.
	c.srv.AgentDir = filepath.Join(dir, "empty")
	code, body, _ := c.raw("/agent/linux-arm64", "Authorization", "Bearer "+token)
	if code != 503 || !strings.Contains(string(body), "linux-arm64") {
		t.Fatalf("missing binary: %d %s", code, body)
	}
}

// The script is what installs a machine, so a test runs it: on a fake machine, with a fake
// agent and a fake systemd, and checks what it left behind.
func TestInstallScriptInstallsAMachine(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the installer runs on Linux")
	}
	if os.Geteuid() != 0 {
		t.Skip("the installer refuses to run without root, as it should")
	}
	c := newClient(t)
	c.login()
	token := c.project()["token"].(string)

	// A fake agent: the installer only ever calls "detect … -write FILE".
	// The installer calls it twice: "-version", to see that what it downloaded runs at all, and
	// "detect ... -write FILE".
	fakeAgent := `#!/bin/sh
name=""
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -version) echo "0.1.0-test"; exit 0 ;;
    -name) name="$2"; shift 2 ;;
    -write) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
echo "looked at the machine"
printf 'server: here\nhost: %s\nsources:\n  - type: host\n' "$name" > "$out"
`
	agents := t.TempDir()
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		if err := os.WriteFile(filepath.Join(agents, "slowpoke-agent-"+platform), []byte(fakeAgent), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c.srv.AgentDir = agents

	// A fake machine: stub commands, and every path the script writes to under a temp root.
	root := t.TempDir()
	stubs := filepath.Join(root, "bin")
	if err := os.MkdirAll(stubs, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "systemctl.log")
	for name, body := range map[string]string{
		"systemctl": "#!/bin/sh\necho \"$@\" >> " + calls + "\n",
		"useradd":   "#!/bin/sh\nexit 0\n",
		"getent":    "#!/bin/sh\nexit 1\n", // no adm group on this machine
	} {
		if err := os.WriteFile(filepath.Join(stubs, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	systemd := filepath.Join(root, "run-systemd")
	if err := os.MkdirAll(systemd, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "usr-local-bin", "slowpoke-agent")
	lib := filepath.Join(root, "usr-local-lib-slowpoke")
	config := filepath.Join(root, "etc", "slowpoke", "agent.yaml")
	unit := filepath.Join(root, "units", "slowpoke-agent.service")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}

	_, script, _ := c.raw("/install.sh")
	path := filepath.Join(root, "install.sh")
	if err := os.WriteFile(path, script, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("sh", append([]string{path}, args...)...)
		cmd.Env = append(os.Environ(),
			"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
			"SLOWPOKE_BIN="+bin, "SLOWPOKE_LIB="+lib, "SLOWPOKE_CONFIG="+config, "SLOWPOKE_UNIT="+unit,
			"SLOWPOKE_STATE="+filepath.Join(root, "state"), "SLOWPOKE_SYSTEMD_DIR="+systemd)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("--token", token, "--name", "web-9", "--server", c.base,
		"--env", "SLOWPOKE_DB_PASSWORD=s3cret", "--env=SLOWPOKE_REDIS_PASSWORD=r3d")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	installed, err := os.ReadFile(bin)
	if err != nil || string(installed) != fakeAgent {
		t.Fatalf("the agent was not installed: %v", err)
	}
	if info, err := os.Stat(bin); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("agent mode: %v %v", info.Mode(), err)
	}
	written, err := os.ReadFile(config)
	if err != nil || !strings.Contains(string(written), "host: web-9") {
		t.Fatalf("the agent did not write its own configuration: %v %s", err, written)
	}
	if strings.Contains(string(written), "auto_update") {
		t.Fatalf("nobody asked for auto-update:\n%s", written)
	}
	if info, _ := os.Stat(config); info.Mode().Perm() != 0o640 {
		t.Fatalf("the configuration holds the token: mode %v", info.Mode().Perm())
	}
	service, err := os.ReadFile(unit)
	if err != nil || !strings.Contains(string(service), "ExecStart="+bin+" -config "+config) {
		t.Fatalf("unit: %v\n%s", err, service)
	}
	// Without auto-update nothing of the machine is writable by the agent.
	if strings.Contains(string(service), "ReadWritePaths") {
		t.Fatalf("the agent can write somewhere nobody asked for:\n%s", service)
	}
	// Passwords live next to the configuration, in a file only the agent's group can read.
	envFile := filepath.Join(filepath.Dir(config), "agent.env")
	secrets, err := os.ReadFile(envFile)
	if err != nil || !strings.Contains(string(secrets), "SLOWPOKE_DB_PASSWORD=s3cret") || !strings.Contains(string(secrets), "SLOWPOKE_REDIS_PASSWORD=r3d") {
		t.Fatalf("passwords: %v\n%s", err, secrets)
	}
	if info, _ := os.Stat(envFile); info.Mode().Perm() != 0o640 {
		t.Fatalf("the password file is readable: mode %v", info.Mode().Perm())
	}
	if !strings.Contains(string(service), "EnvironmentFile=-"+envFile) {
		t.Fatalf("the unit does not read the passwords:\n%s", service)
	}

	done, _ := os.ReadFile(calls)
	for _, want := range []string{"daemon-reload", "enable slowpoke-agent", "restart slowpoke-agent"} {
		if !strings.Contains(string(done), want) {
			t.Fatalf("systemd was not asked to %q:\n%s", want, done)
		}
	}
	if !strings.Contains(out, c.base) {
		t.Fatalf("the installer does not say where to look:\n%s", out)
	}

	// Run again: it upgrades the agent and keeps the configuration that is already there.
	if err := os.WriteFile(config, []byte("server: here\nhost: kept-by-hand\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if out, err := run("--token", token, "--server", c.base); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if again, _ := os.ReadFile(config); !strings.Contains(string(again), "kept-by-hand") {
		t.Fatalf("a second install overwrote the configuration: %s", again)
	}
	// A second --env changes one password and leaves the others alone.
	if out, err := run("--token", token, "--server", c.base, "--env", "SLOWPOKE_DB_PASSWORD=changed"); err != nil {
		t.Fatalf("env run: %v\n%s", err, out)
	}
	secrets, _ = os.ReadFile(envFile)
	if !strings.Contains(string(secrets), "SLOWPOKE_DB_PASSWORD=changed") || !strings.Contains(string(secrets), "SLOWPOKE_REDIS_PASSWORD=r3d") ||
		strings.Contains(string(secrets), "s3cret") {
		t.Fatalf("passwords after the second run:\n%s", secrets)
	}

	if out, err := run("--token", token, "--server", c.base, "--force"); err != nil {
		t.Fatalf("forced run: %v\n%s", err, out)
	}
	if again, _ := os.ReadFile(config); strings.Contains(string(again), "kept-by-hand") {
		t.Fatal("--force must write a new configuration")
	}

	// An agent that replaces itself needs a place it can write: with auto-update the binary lives
	// in its own directory, owned by the agent's user and opened in the unit, and the command on
	// PATH becomes a link to it. Without it, /usr stays read-only for the agent, and the agent
	// would have written "update failed" in its log forever.
	if out, err := run("--token", token, "--server", c.base, "--auto-update"); err != nil {
		t.Fatalf("auto-update run: %v\n%s", err, out)
	}
	real := filepath.Join(lib, "slowpoke-agent")
	if info, err := os.Lstat(bin); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a link to the agent: %v", bin, err)
	}
	if target, _ := os.Readlink(bin); target != real {
		t.Fatalf("the link points at %q, not at %q", target, real)
	}
	if installed, err := os.ReadFile(real); err != nil || string(installed) != fakeAgent {
		t.Fatalf("the agent is not in its own directory: %v", err)
	}
	service, _ = os.ReadFile(unit)
	if !strings.Contains(string(service), "ExecStart="+real+" -config "+config) || !strings.Contains(string(service), "ReadWritePaths="+lib) {
		t.Fatalf("the unit does not let the agent replace itself:\n%s", service)
	}
	if written, _ := os.ReadFile(config); !strings.Contains(string(written), "auto_update: true") {
		t.Fatalf("--auto-update must be in the configuration:\n%s", written)
	}

	// And back: an install without --auto-update leaves one binary again, owned by root.
	if out, err := run("--token", token, "--server", c.base); err != nil {
		t.Fatalf("back to a plain install: %v\n%s", err, out)
	}
	if info, err := os.Lstat(bin); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is still a link: %v", bin, err)
	}
	if service, _ := os.ReadFile(unit); strings.Contains(string(service), "ReadWritePaths") {
		t.Fatalf("the agent still writes where it did:\n%s", service)
	}

	// What actually happened on one of our machines: the server handed out something that was not
	// an agent, and it went into /usr/local/bin. The installer runs what it downloaded before
	// putting it in place, and leaves the working agent alone when that fails.
	good, _ := os.ReadFile(bin)
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		// It runs, and it fails: a truncated download, the wrong architecture, a stub.
		if err := os.WriteFile(filepath.Join(agents, "slowpoke-agent-"+platform), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out, err = run("--token", token, "--server", c.base, "--force")
	if err == nil {
		t.Fatalf("the installer accepted something that is not an agent:\n%s", out)
	}
	if !strings.Contains(out, "agent") {
		t.Fatalf("the installer must say what went wrong:\n%s", out)
	}
	if still, _ := os.ReadFile(bin); string(still) != string(good) {
		t.Fatalf("a failed install replaced the working agent: %q", still)
	}
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		if err := os.WriteFile(filepath.Join(agents, "slowpoke-agent-"+platform), []byte(fakeAgent), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if out, err := run("--uninstall"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	for _, left := range []string{bin, real, unit, config} {
		if _, err := os.Stat(left); !os.IsNotExist(err) {
			t.Fatalf("%s survived the uninstall", left)
		}
	}
}

// Every answer to an agent says which version this server is, and where its own agent for that
// machine can be found: that is all an agent needs to keep itself aligned.
func TestTheServerTellsAgentsWhichVersionItIs(t *testing.T) {
	c := newClient(t)
	c.login()
	token := c.project()["token"].(string)

	ingest := func(platform string) map[string]any {
		body, _ := json.Marshal(protocol.Payload{BatchID: platform + time.Now().String(), Agent: protocol.AgentInfo{Host: "web-1", Kind: "agent"}})
		req, _ := http.NewRequest("POST", c.base+"/api/v1/ingest", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if platform != "" {
			req.Header.Set("X-Slowpoke-Platform", platform)
		}
		res, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		raw, _ := io.ReadAll(res.Body)
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%d %s", res.StatusCode, raw)
		}
		return out
	}

	// A server that does not carry the binaries says its version and nothing else.
	answer := ingest("linux-amd64")
	if answer["server_version"] != Version {
		t.Fatalf("version: %v", answer)
	}
	if _, ok := answer["agent_download"]; ok {
		t.Fatalf("this server has no agent to hand out: %v", answer)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "slowpoke-agent-linux-amd64"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.srv.AgentDir = dir

	if answer = ingest("linux-amd64"); answer["agent_download"] != "/agent/linux-amd64" {
		t.Fatalf("download: %v", answer)
	}
	// Nothing for a machine this server has no binary for, and nothing for an agent that does
	// not say what it runs on (an older one, or the Laravel package).
	if answer = ingest("linux-arm64"); answer["agent_download"] != nil {
		t.Fatalf("arm64: %v", answer)
	}
	if answer = ingest(""); answer["agent_download"] != nil {
		t.Fatalf("no platform: %v", answer)
	}
}

// A machine of ours ended up with the panel's own index page installed as its agent: an older
// server answered /agent/linux-amd64 with its web page, and 780 bytes of HTML went into
// /usr/local/bin. Nothing on either side noticed. Both sides check now: the server refuses to
// hand out a file that cannot be an agent, whatever is sitting in its agent directory.
func TestTheServerNeverHandsOutSomethingThatIsNotAnAgent(t *testing.T) {
	c := newClient(t)
	c.login()
	token := c.project()["token"].(string)

	dir := t.TempDir()
	c.srv.AgentDir = dir
	page := []byte("<!doctype html>\n<html><body>the panel</body></html>\n")
	if err := os.WriteFile(filepath.Join(dir, "slowpoke-agent-linux-amd64"), page, 0o755); err != nil {
		t.Fatal(err)
	}

	code, body, _ := c.raw("/agent/linux-amd64", "Authorization", "Bearer "+token)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("the server handed out a web page as an agent: %d %s", code, body)
	}
	if !strings.Contains(string(body), "linux-amd64") {
		t.Fatalf("the answer must say what is wrong: %s", body)
	}
	// The checksum of something that is not an agent is just as useless.
	if code, _, _ := c.raw("/agent/linux-amd64.sha256", "Authorization", "Bearer "+token); code != http.StatusServiceUnavailable {
		t.Fatalf("checksum of a non-agent: %d", code)
	}

	// A real agent is served as before.
	if err := os.WriteFile(filepath.Join(dir, "slowpoke-agent-linux-amd64"), fakeAgentSaying("0.1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := c.raw("/agent/linux-amd64", "Authorization", "Bearer "+token); code != 200 {
		t.Fatalf("a real agent must still be served: %d", code)
	}
}

// fakeAgentSaying is a file the server is willing to hand out and a machine is able to run.
func fakeAgentSaying(version string) []byte {
	return []byte("#!/bin/sh\necho " + version + "\n")
}
