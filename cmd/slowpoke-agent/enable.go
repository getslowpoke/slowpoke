package main

// slowpoke-agent enable-duration: the one configuration change Slowpoke asks for, done by the
// agent instead of by a person at 23:00 on the only production server they have. It shows what
// it will write, writes it, asks nginx whether it likes it, reloads, checks the sites still
// answer exactly as they answered a second earlier, and puts everything back if anything at all
// is not right.

import (
	"bufio"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/agent/detect"
)

func runEnableDuration(args []string) int {
	fs := flag.NewFlagSet("enable-duration", flag.ExitOnError)
	root := fs.String("root", "/", "filesystem root to work on (for containers mounting the host)")
	dry := fs.Bool("dry-run", false, "print what would be written and stop")
	undo := fs.Bool("undo", false, "put back what the last run changed")
	yes := fs.Bool("yes", false, "do not ask")
	backup := fs.String("backup", "/var/lib/slowpoke/nginx-backup", "where the previous files are kept")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: slowpoke-agent enable-duration [-dry-run] [-yes] [-undo]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	run := detect.Runner{
		Test:   func() error { return command("nginx", "-t") },
		Reload: reloadNginx,
	}

	if *undo {
		if err := detect.Undo(*backup, run); err != nil {
			fmt.Fprintln(os.Stderr, "undo:", err)
			return 1
		}
		fmt.Println("Everything is back the way it was, and nginx reloaded.")
		return 0
	}

	report := detect.Scan(*root)
	changes, err := detect.PlanDuration(*root, report)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(changes) == 0 {
		fmt.Println("Every site here already says how long its requests take: nothing to do.")
		return 0
	}

	fmt.Println("This is what it writes:")
	for _, c := range changes {
		fmt.Printf("\n  %s%s\n  %s\n", c.Path, map[bool]string{true: "  (new file)"}[c.New], c.Why)
		for _, line := range added(c.Before, c.After) {
			fmt.Println("    + " + line)
		}
	}
	fmt.Println("\nNothing else is touched. The logs these sites already write keep receiving every request.")
	if *dry {
		return 0
	}
	if !*yes && !confirm() {
		fmt.Println("Nothing was changed.")
		return 0
	}

	// What the sites answer right now, so the same question can be asked after the reload.
	before := probe(report)
	run.Verify = func() error {
		time.Sleep(500 * time.Millisecond)
		for site, was := range before {
			if now := ask(site); now != was {
				return fmt.Errorf("%s answered %s before and %s now", site, was, now)
			}
		}
		return nil
	}

	if err := detect.Apply(changes, *backup, run); err != nil {
		fmt.Fprintln(os.Stderr, "\n"+err.Error())
		fmt.Fprintln(os.Stderr, "Everything is back the way it was.")
		return 1
	}
	fmt.Println("\nDone: nginx accepted it, reloaded, and the sites answer exactly as before.")
	fmt.Println("The agent picks up the new logs by itself within a minute.")
	fmt.Println("To go back:  slowpoke-agent enable-duration -undo")
	return 0
}

// added lists the lines the change adds, which is all a person needs to see.
func added(before, after string) []string {
	old := map[string]int{}
	for _, l := range strings.Split(before, "\n") {
		old[strings.TrimSpace(l)]++
	}
	var out []string
	for _, l := range strings.Split(after, "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if old[t] > 0 {
			old[t]--
			continue
		}
		out = append(out, t)
	}
	return out
}

func confirm() bool {
	fmt.Print("\nWrite it? [y/N] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func command(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func reloadNginx() error {
	if err := command("systemctl", "reload", "nginx"); err == nil {
		return nil
	}
	return command("nginx", "-s", "reload")
}

// probe asks every site on this machine what it answers, over the loopback with its own name in
// the Host header: no traffic leaves the machine and nothing is written.
func probe(r detect.Report) map[string]string {
	out := map[string]string{}
	for _, site := range r.Sites {
		if site.Name == "" {
			continue
		}
		out[site.Name] = ask(site.Name)
	}
	return out
}

var client = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	// The certificate is for the site's name and we are calling 127.0.0.1 on purpose: this asks
	// the nginx on this machine, it never leaves it.
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

func ask(site string) string {
	answers := make([]string, 0, 2)
	for _, base := range []string{"http://127.0.0.1", "https://127.0.0.1"} {
		req, err := http.NewRequest(http.MethodGet, base+"/", nil)
		if err != nil {
			continue
		}
		req.Host = site
		res, err := client.Do(req)
		if err != nil {
			answers = append(answers, "unreachable")
			continue
		}
		res.Body.Close()
		answers = append(answers, fmt.Sprint(res.StatusCode))
	}
	return strings.Join(answers, "/")
}
