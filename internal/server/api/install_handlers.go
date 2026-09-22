package api

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// installScript is served at /install.sh: one command installs an agent on a machine, and the
// agent itself decides what to read there. The script carries no secret: the token is an argument.
//
//go:embed install.sh
var installScript string

// fleetScript is served at /fleet.sh: it runs on somebody's laptop, with their ssh keys, and
// installs the agent on the machines the panel can see but nobody measures.
//
//go:embed fleet.sh
var fleetScript string

// agentPlatforms are the binaries a server can hand out. They are built with the server, so a
// machine always gets the agent of the version it talks to.
var agentPlatforms = map[string]bool{"linux-amd64": true, "linux-arm64": true}

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, strings.ReplaceAll(installScript, "@@SERVER@@", s.publicURL(r)))
}

func (s *Server) handleFleetScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, strings.ReplaceAll(fleetScript, "@@SERVER@@", s.publicURL(r)))
}

// handleAgentBinary serves the agent, or its checksum, to a machine that already has a token:
// nothing about the installation is anonymous except the script itself.
func (s *Server) handleAgentBinary(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.agentAuth(w, r); !ok {
		return
	}
	name := r.PathValue("platform")
	platform := strings.TrimSuffix(name, ".sha256")
	if !agentPlatforms[platform] {
		writeError(w, http.StatusNotFound, "no agent for "+platform+": build it from source (docs/install/agent.md)")
		return
	}
	path := filepath.Join(s.AgentDir, "slowpoke-agent-"+platform)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		writeError(w, http.StatusServiceUnavailable,
			"this server does not carry the agent for "+platform+": put slowpoke-agent-"+platform+" in "+s.AgentDir+" (SLOWPOKE_AGENT_DIR)")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		serverError(w, err)
		return
	}
	// A machine once installed the panel's own index page as its agent, because an older server
	// answered this path with its web page. Whatever sits in the agent directory, only something
	// a machine can actually execute leaves this handler.
	if !looksExecutable(f) {
		writeError(w, http.StatusServiceUnavailable,
			"what this server carries for "+platform+" is not an agent binary: put the real slowpoke-agent-"+platform+" in "+s.AgentDir+" (SLOWPOKE_AGENT_DIR)")
		return
	}

	if strings.HasSuffix(name, ".sha256") {
		sum := sha256.New()
		if _, err := io.Copy(sum, f); err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, hex.EncodeToString(sum.Sum(nil))+"  slowpoke-agent-"+platform+"\n")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="slowpoke-agent"`)
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// looksExecutable answers whether the file begins like something a Linux machine can run: the
// agent is an ELF binary, and a wrapper script around it is a fair thing to put there too. A web
// page is neither. The file is left at the beginning again for whoever reads it next.
func looksExecutable(f *os.File) bool {
	var head [4]byte
	n, err := f.ReadAt(head[:], 0)
	if err != nil || n != len(head) {
		return false
	}
	_, _ = f.Seek(0, io.SeekStart)
	return head == [4]byte{0x7f, 'E', 'L', 'F'} || string(head[:2]) == "#!"
}

// agentDownload is the path where this server hands out the agent for that platform, empty when
// it does not carry it (a server installed as a binary without the agents next to it).
func (s *Server) agentDownload(platform string) string {
	if !agentPlatforms[platform] || s.AgentDir == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(s.AgentDir, "slowpoke-agent-"+platform)); err != nil {
		return ""
	}
	return "/agent/" + platform
}
