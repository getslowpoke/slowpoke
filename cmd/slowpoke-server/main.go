// Command slowpoke-server runs the Slowpoke API, UI and background workers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"golang.org/x/term"

	"github.com/getslowpoke/slowpoke/internal/server/api"
	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/demo"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/retention"
	"github.com/getslowpoke/slowpoke/internal/server/store"
	"github.com/getslowpoke/slowpoke/internal/server/tokens"
	"github.com/getslowpoke/slowpoke/internal/server/web"
)

var version = "0.1.0-dev"

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

func main() {
	level := slog.LevelInfo
	if env("SLOWPOKE_LOG_LEVEL", "info") == "debug" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
	api.Version = version

	command, args := commandFrom(os.Args[1:])
	if command == "" {
		usage()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if command == "healthcheck" || command == "version" {
		if command == "version" {
			fmt.Println(version)
			return
		}
		res, err := http.Get("http://127.0.0.1" + env("SLOWPOKE_LISTEN", ":8080") + "/healthz")
		if err != nil || res.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}

	db, err := store.Open(env("SLOWPOKE_DATABASE_URL", "sqlite://./slowpoke.db"))
	if err != nil {
		fatal("cannot open database", err)
	}
	defer db.Close()
	if err := waitForDatabase(ctx, db); err != nil {
		fatal("database unreachable", err)
	}
	if err := db.Migrate(ctx); err != nil {
		fatal("migration failed", err)
	}

	switch command {
	case "serve":
		serve(ctx, db)
	case "migrate":
		slog.Info("database is up to date")
	case "seed-demo":
		fs := flag.NewFlagSet("seed-demo", flag.ExitOnError)
		days := fs.Int("days", 30, "days of history")
		name := fs.String("name", "Demo Shop", "project name")
		_ = fs.Parse(args)
		start := time.Now()
		p, err := demo.Seed(ctx, db, demo.Options{Days: *days, Name: *name})
		if err != nil {
			fatal("seed failed", err)
		}
		slog.Info("demo project ready", "slug", p.Slug, "token", p.Token, "took", time.Since(start).Round(time.Second).String())
	case "create-user":
		fs := flag.NewFlagSet("create-user", flag.ExitOnError)
		email := fs.String("email", "", "email")
		name := fs.String("name", "", "name")
		password := fs.String("password", os.Getenv("SLOWPOKE_PASSWORD"), "password (or SLOWPOKE_PASSWORD)")
		_ = fs.Parse(args)
		srv := api.New(db, nil)
		u, err := srv.Auth.CreateUser(ctx, *name, *email, *password)
		if err != nil {
			fatal("cannot create user", err)
		}
		slog.Info("user created", "email", u.Email)
	case "set-password":
		fs := flag.NewFlagSet("set-password", flag.ExitOnError)
		email := fs.String("email", "", "the address of the person who needs a new password")
		password := fs.String("password", "", "the new password; better left out and typed at the prompt")
		_ = fs.Parse(args)
		plain, err := newPassword(*password, os.Getenv("SLOWPOKE_PASSWORD"), askPassword)
		if err != nil {
			fatal("cannot set the password", err)
		}
		u, err := auth.Service{DB: db}.SetPassword(ctx, *email, plain)
		if err != nil {
			fatal("cannot set the password", err)
		}
		fmt.Printf("new password for %s; every session of that account is now closed\n", u.Email)
	case "create-project":
		fs := flag.NewFlagSet("create-project", flag.ExitOnError)
		name := fs.String("name", "", "project name")
		tz := fs.String("timezone", "UTC", "IANA timezone")
		_ = fs.Parse(args)
		p, err := project.Repo{DB: db}.Create(ctx, *name, *tz)
		if err != nil {
			fatal("cannot create project", err)
		}
		fmt.Println(p.Token)
	case "list-projects":
		projects, err := project.Repo{DB: db}.List(ctx)
		if err != nil {
			fatal("cannot list projects", err)
		}
		for _, p := range projects {
			fmt.Printf("%s\t%s\t%s\n", p.Slug, p.Timezone, p.Name)
		}
	case "requeue-dead":
		n, err := ingest.NewQueue(db).RequeueDead(ctx)
		if err != nil {
			fatal("cannot requeue", err)
		}
		fmt.Printf("%d payloads back in the queue\n", n)

	case "create-token":
		fs := flag.NewFlagSet("create-token", flag.ExitOnError)
		slug := fs.String("project", "", "project slug (see list-projects)")
		kind := fs.String("kind", tokens.KindAgent, "agent (one per machine) or personal (one per person, needs -user)")
		name := fs.String("name", "", "machine or purpose, shown in Settings → Access")
		email := fs.String("user", "", "the person a personal token belongs to")
		_ = fs.Parse(args)
		var userID int64
		switch *kind {
		case tokens.KindAgent:
		case tokens.KindPersonal:
			if *email == "" {
				fatal("cannot create token", errors.New("a personal token belongs to a person: pass -user their@address"))
			}
			u, err := auth.Service{DB: db}.UserByEmail(ctx, *email)
			if err != nil {
				fatal("cannot create token", err)
			}
			userID = u.ID
		default:
			fatal("cannot create token", errors.New("kind is agent or personal"))
		}
		p, err := project.Repo{DB: db}.BySlug(ctx, *slug)
		if err != nil {
			fatal("unknown project "+*slug, err)
		}
		_, plain, err := tokens.Service{DB: db}.Create(ctx, p.ID, *kind, *name, userID)
		if err != nil {
			fatal("cannot create token", err)
		}
		fmt.Println(plain)
	case "version":
		fmt.Println(version)
	default:
		usage()
	}
}

// newPassword works out the new password, in the order that keeps it quietest: the flag (visible
// in `ps` and in the shell history, so only when somebody asks for it), then SLOWPOKE_PASSWORD
// for a script, then the terminal, which asks twice because nobody can see what they typed.
func newPassword(flag, env string, ask func(prompt string) (string, error)) (string, error) {
	switch {
	case flag != "":
		return flag, auth.CheckPassword(flag)
	case env != "":
		return env, auth.CheckPassword(env)
	case ask == nil:
		return "", errors.New("no terminal to type it on: pass it in SLOWPOKE_PASSWORD, or with -password if you accept that it stays in the shell history")
	}
	first, err := ask("New password: ")
	if err != nil {
		return "", err
	}
	if err := auth.CheckPassword(first); err != nil {
		return "", err
	}
	again, err := ask("Again: ")
	if err != nil {
		return "", err
	}
	if first != again {
		return "", errors.New("the two passwords are not the same")
	}
	return first, nil
}

// askPassword reads one password from the terminal without echoing it. It is nil when standard
// input is not a terminal, so a script pipes nothing and gets an explanation instead of a hang.
var askPassword = func() func(string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil
	}
	return func(prompt string) (string, error) {
		fmt.Fprint(os.Stderr, prompt)
		typed, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(typed), err
	}
}()

// commandFrom reads the subcommand. Flags with no verb still mean serve, the way the unit files
// are written; no arguments at all means the person wants to know what this binary does.
func commandFrom(args []string) (string, []string) {
	switch {
	case len(args) == 0:
		return "", nil
	case strings.HasPrefix(args[0], "-"):
		return "serve", args
	default:
		return args[0], args[1:]
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: slowpoke-server [serve|migrate|healthcheck|version|create-user|set-password|create-project|list-projects|create-token|requeue-dead|seed-demo]\n")
	os.Exit(2)
}

func waitForDatabase(ctx context.Context, db *store.DB) error {
	var err error
	for i := 0; i < 30; i++ {
		if err = db.PingContext(ctx); err == nil {
			return nil
		}
		slog.Warn("waiting for the database", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return err
}

func serve(ctx context.Context, db *store.DB) {
	role := env("SLOWPOKE_ROLE", "all") // all | web | worker
	publicURL := env("SLOWPOKE_PUBLIC_URL", "")

	srv := api.New(db, web.FS())
	// La lingua in cui si apre il pannello quando chi guarda non ne ha ancora scelta una: serve a
	// un'installazione pubblica, dove il browser di chi passa non dice niente su di noi.
	srv.Locale = env("SLOWPOKE_LOCALE", "")
	// A public demo: its credentials on the login page and a link that logs straight in.
	srv.DemoEmail, srv.DemoPassword = env("SLOWPOKE_DEMO_EMAIL", ""), env("SLOWPOKE_DEMO_PASSWORD", "")
	srv.DemoProject = env("SLOWPOKE_DEMO_LIVE", "")
	srv.MaxQueue = envInt("SLOWPOKE_MAX_QUEUE", 20000)
	srv.SecureCookies = strings.HasPrefix(publicURL, "https://")
	srv.Notifier.PublicURL = strings.TrimRight(publicURL, "/")
	srv.PublicURL = strings.TrimRight(publicURL, "/")
	proxies, err := api.ParseTrustedProxies(env("SLOWPOKE_TRUSTED_PROXIES", ""))
	if err != nil {
		fatal("invalid SLOWPOKE_TRUSTED_PROXIES", err)
	}
	srv.TrustedProxies = proxies
	// The agents this server hands out to the machines it measures, one binary per platform.
	srv.AgentDir = env("SLOWPOKE_AGENT_DIR", "/usr/local/share/slowpoke")
	// Asked only when somebody presses the button in Settings; empty turns the button off.
	srv.UpdateFeed = env("SLOWPOKE_UPDATE_FEED", api.DefaultUpdateFeed)

	if role == "all" || role == "worker" {
		workers := envInt("SLOWPOKE_WORKERS", 4)
		go srv.Worker.Run(ctx, workers, 500*time.Millisecond)
		go srv.Engine.Run(ctx, time.Minute)
		go srv.Notifier.Run(ctx, 30*time.Second)
		go retention.Run(ctx, db, retention.Defaults())
		slog.Info("background workers started", "ingest_workers", workers)
	}
	if slug := env("SLOWPOKE_DEMO_LIVE", ""); slug != "" && role != "web" {
		go demo.RunLive(ctx, db, slug)
		slog.Info("live demo data enabled", "project", slug)
	}
	if role == "worker" {
		<-ctx.Done()
		return
	}

	httpServer := &http.Server{
		Addr:              env("SLOWPOKE_LISTEN", ":8080"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	slog.Info("slowpoke server listening", "addr", httpServer.Addr, "role", role, "version", version, "database", string(db.Dialect))
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("http server", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}
