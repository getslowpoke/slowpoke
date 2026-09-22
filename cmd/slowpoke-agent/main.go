package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	_ "time/tzdata"

	"github.com/getslowpoke/slowpoke/internal/agent"
	"github.com/getslowpoke/slowpoke/internal/agent/detect"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "detect" {
		os.Exit(runDetect(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "enable-duration" {
		os.Exit(runEnableDuration(os.Args[2:]))
	}
	configPath := flag.String("config", "/etc/slowpoke/agent.yaml", "configuration file")
	version := flag.Bool("version", false, "print the version")
	flag.Parse()

	if *version {
		fmt.Println(agent.Version)
		return
	}

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	for _, w := range agent.ConfigWarnings(*configPath) {
		slog.Warn(w)
	}
	runtime.GOMAXPROCS(cfg.MaxProcs)
	debug.SetMemoryLimit(int64(cfg.MemoryLimit) << 20)

	runner, err := agent.NewRunner(cfg)
	if err != nil {
		slog.Error("cannot start", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner.Run(ctx)
}

// runDetect prints what the agent can read on this machine and what to change, and writes a
// configuration on request. It never changes anything else.
func runDetect(args []string) int {
	fs := flag.NewFlagSet("detect", flag.ExitOnError)
	root := fs.String("root", "/", "filesystem root to inspect (for containers mounting the host)")
	server := fs.String("server", "", "Slowpoke server address for the generated configuration")
	token := fs.String("token", "", "agent token for the generated configuration (default ${SLOWPOKE_TOKEN})")
	name := fs.String("name", "", "name of this machine in the panel (default: its hostname)")
	write := fs.String("write", "", "write the configuration to this file instead of printing it")
	force := fs.Bool("force", false, "overwrite an existing configuration file")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: slowpoke-agent detect [-server URL] [-token spa_…] [-name machine] [-write /etc/slowpoke/agent.yaml]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	report := detect.Scan(*root)
	if *name != "" {
		report.Host = *name
	}
	fmt.Print(report.Text())
	config := report.Config(*server, *token)

	if *write == "" {
		fmt.Println()
		fmt.Println("CONFIGURATION (save as /etc/slowpoke/agent.yaml, or run again with -write)")
		fmt.Println()
		fmt.Print(config)
		return 0
	}
	if _, err := os.Stat(*write); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "\n%s already exists: not overwritten (use -force)\n", *write)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(*write), 0o750); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// The file holds the token: readable by root and the agent's group only.
	if err := os.WriteFile(*write, []byte(config), 0o640); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("\nConfiguration written to %s\n", *write)
	return 0
}
