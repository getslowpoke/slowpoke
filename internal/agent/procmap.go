package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Reading /proc to know which process holds a socket costs one readlink per open file
// descriptor of the machine. It is bounded: the agent stops as soon as it has found every
// socket it asked about, and gives up after these limits rather than competing with the app.
const (
	maxReadlinks = 20000
	procBudget   = 300 * time.Millisecond
)

// processesForInodes maps socket inodes to the name of the process holding them, reading
// /proc/<pid>/fd. Sockets nobody asked about are never looked up.
func processesForInodes(procRoot string, inodes map[uint64]bool) map[uint64]string {
	out := map[uint64]string{}
	if len(inodes) == 0 {
		return out
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}
	deadline := time.Now().Add(procBudget)
	links := 0
	names := map[string]string{} // pid -> process name, read once

	for _, e := range entries {
		if len(out) == len(inodes) || links >= maxReadlinks || time.Now().After(deadline) {
			break
		}
		pid := e.Name()
		if !e.IsDir() || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(procRoot, pid, "fd"))
		if err != nil {
			continue // a process that ended, or one we may not read
		}
		for _, fd := range fds {
			if links >= maxReadlinks {
				break
			}
			links++
			target, err := os.Readlink(filepath.Join(procRoot, pid, "fd", fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if !ok || !inodes[inode] {
				continue
			}
			name, known := names[pid]
			if !known {
				name = processName(procRoot, pid)
				names[pid] = name
			}
			if name != "" {
				out[inode] = name
			}
		}
	}
	return out
}

// socketInode reads the "socket:[12345]" target of a file descriptor.
func socketInode(target string) (uint64, bool) {
	rest, ok := strings.CutPrefix(target, "socket:[")
	if !ok {
		return 0, false
	}
	rest = strings.TrimSuffix(rest, "]")
	n, err := strconv.ParseUint(rest, 10, 64)
	return n, err == nil
}

// processName is what a person would call that process: the PHP-FPM pool, the artisan command,
// the node script, the database, or simply the program name.
func processName(procRoot, pid string) string {
	comm := strings.TrimSpace(readFile(filepath.Join(procRoot, pid, "comm")))
	raw := readFile(filepath.Join(procRoot, pid, "cmdline"))
	args := splitArgs(raw)
	line := strings.Join(args, " ")

	switch {
	case strings.HasPrefix(line, "php-fpm: pool "):
		return "php-fpm:" + strings.TrimSpace(strings.TrimPrefix(line, "php-fpm: pool "))
	case strings.HasPrefix(line, "php-fpm:"):
		return "php-fpm"
	case strings.HasPrefix(comm, "nginx"), strings.HasPrefix(line, "nginx:"):
		return "nginx"
	case strings.HasPrefix(comm, "apache"), comm == "httpd":
		return "apache"
	case comm == "mysqld", comm == "mariadbd", strings.HasPrefix(comm, "mysqld"):
		return "mysql"
	case strings.HasPrefix(comm, "postgres"):
		return "postgres"
	case strings.HasPrefix(comm, "redis"):
		return "redis"
	case strings.HasPrefix(comm, "valkey"):
		return "redis"
	}

	// Interpreters: the script is what matters, not the binary.
	base := filepath.Base(firstOr(args, comm))
	switch {
	case strings.HasPrefix(base, "php"):
		if cmd := artisanCommand(args); cmd != "" {
			return "php artisan " + cmd
		}
	case base == "node", base == "nodejs", strings.HasPrefix(base, "python"), base == "ruby":
		if script := firstScript(args[1:]); script != "" {
			return base + " " + filepath.Base(script)
		}
	}
	if comm != "" {
		return comm
	}
	return base
}

// artisanCommand finds "artisan <command>" in a PHP command line (queue workers, schedulers).
func artisanCommand(args []string) string {
	for i, a := range args {
		if filepath.Base(a) == "artisan" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func firstScript(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func firstOr(args []string, fallback string) string {
	if len(args) > 0 && args[0] != "" {
		return args[0]
	}
	return fallback
}

func splitArgs(raw string) []string {
	var out []string
	for _, a := range strings.Split(raw, "\x00") {
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

func readFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
