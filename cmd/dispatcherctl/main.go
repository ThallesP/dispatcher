package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultDispatcherURL = "http://localhost:8090"

var version = "dev"

type command struct {
	method       string
	path         string
	requiresAuth bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	return runWithBrowser(args, getenv, os.Stdin, stdout, stderr, openBrowser)
}

func runWithBrowser(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer, openURL func(string) error) int {
	flags := flag.NewFlagSet("dispatcherctl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	baseURL := flags.String("url", strings.TrimSpace(getenv("DISPATCHER_URL")), "Dispatcher instance URL")
	configPath := flags.String("config", getenv("DISPATCHER_CONFIG"), "credentials file path")
	compact := flags.Bool("compact", false, "emit compact JSON")
	timeout := flags.Duration("timeout", 90*time.Second, "HTTP request timeout")
	flags.Usage = func() { printUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return 2
	}

	rest := flags.Args()
	if len(rest) == 0 {
		printUsage(stderr)
		return 2
	}
	if rest[0] == "help" {
		printUsage(stdout)
		return 0
	}
	if rest[0] == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "error: timeout must be greater than zero")
		return 2
	}
	if rest[0] == "login" && len(rest) != 1 {
		fmt.Fprintln(stderr, "error: login takes no arguments")
		return 2
	}
	if rest[0] == "logout" && len(rest) != 1 {
		fmt.Fprintln(stderr, "error: logout takes no arguments")
		return 2
	}

	credentialsPath, err := resolveCredentialsPath(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	selectedURL, err := resolveDispatcherURL(*baseURL, credentialsPath, rest[0] == "login", stdin, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	client, err := newAPIClient(selectedURL, "", *timeout)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if rest[0] == "login" {
		ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
		defer cancel()
		if err := login(ctx, client, credentialsPath, stdout, openURL); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if rest[0] == "logout" {
		if err := removeStoredSession(credentialsPath, client.baseURL); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Logged out of %s.\n", client.baseURL)
		return 0
	}

	cmd, err := parseCommand(rest, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if cmd.requiresAuth {
		session, ok, err := loadStoredSession(credentialsPath, client.baseURL, time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintf(stderr, "error: not logged in to %s (run dispatcherctl --url %s login)\n", client.baseURL, client.baseURL)
			return 1
		}
		client.session = session.Session
		client.onSession = func(renewed storedSession) error {
			return saveStoredSession(credentialsPath, client.baseURL, renewed)
		}
	}
	body, err := client.request(context.Background(), cmd.method, cmd.path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err := writeOutput(stdout, body, *compact); err != nil {
		fmt.Fprintf(stderr, "error: write output: %v\n", err)
		return 1
	}
	return 0
}

func parseCommand(args []string, stderr io.Writer) (command, error) {
	simple := map[string]command{
		"health":            {method: http.MethodGet, path: "/api/health"},
		"whoami":            {method: http.MethodGet, path: "/api/auth/me", requiresAuth: true},
		"summary":           {method: http.MethodGet, path: "/api/analytics/summary", requiresAuth: true},
		"templates":         {method: http.MethodGet, path: "/api/analytics/templates", requiresAuth: true},
		"notifications":     {method: http.MethodGet, path: "/api/notify/targets", requiresAuth: true},
		"withdraw-settings": {method: http.MethodGet, path: "/api/withdraw/settings", requiresAuth: true},
		"withdraw-accounts": {method: http.MethodGet, path: "/api/withdraw/accounts", requiresAuth: true},
		"refresh":           {method: http.MethodPost, path: "/api/analytics/refresh", requiresAuth: true},
	}
	if cmd, ok := simple[args[0]]; ok {
		if len(args) != 1 {
			return command{}, fmt.Errorf("%s takes no arguments", args[0])
		}
		return cmd, nil
	}

	switch args[0] {
	case "payouts":
		payoutFlags := flag.NewFlagSet("payouts", flag.ContinueOnError)
		payoutFlags.SetOutput(stderr)
		days := payoutFlags.Int("days", 30, "history window in days (1-365)")
		if err := payoutFlags.Parse(args[1:]); err != nil {
			return command{}, err
		}
		if payoutFlags.NArg() != 0 {
			return command{}, fmt.Errorf("payouts takes no positional arguments")
		}
		if *days < 1 || *days > 365 {
			return command{}, fmt.Errorf("days must be between 1 and 365")
		}
		return command{
			method:       http.MethodGet,
			path:         "/api/analytics/payout?days=" + strconv.Itoa(*days),
			requiresAuth: true,
		}, nil
	case "projects":
		projectFlags := flag.NewFlagSet("projects", flag.ContinueOnError)
		projectFlags.SetOutput(stderr)
		days := projectFlags.Int("days", 30, "history window in days (1-365)")
		positional, err := parseInterspersed(projectFlags, args[1:])
		if err != nil {
			return command{}, err
		}
		if len(positional) != 1 {
			return command{}, fmt.Errorf("projects requires exactly one template id or code")
		}
		if *days < 1 || *days > 365 {
			return command{}, fmt.Errorf("days must be between 1 and 365")
		}
		return command{
			method:       http.MethodGet,
			path:         "/api/analytics/templates/" + url.PathEscape(positional[0]) + "/projects?days=" + strconv.Itoa(*days),
			requiresAuth: true,
		}, nil
	case "raw":
		return parseRawCommand(args[1:], stderr)
	case "get":
		if len(args) != 2 {
			return command{}, fmt.Errorf("get requires exactly one API path")
		}
		path := args[1]
		if strings.ContainsAny(path, "\r\n") || strings.Contains(path, "://") {
			return command{}, fmt.Errorf("get expects a path, not a URL")
		}
		if !strings.HasPrefix(path, "/") {
			path = "/api/" + path
		}
		return command{method: http.MethodGet, path: path, requiresAuth: true}, nil
	default:
		return command{}, fmt.Errorf("unknown command %q (run dispatcherctl help)", args[0])
	}
}

// rawDatasets maps the datasets `raw` accepts to their endpoints.
var rawDatasets = map[string]string{
	"snapshots": "/api/raw/snapshots",
	"payouts":   "/api/raw/payouts",
}

// parseRawCommand builds a raw query. Values are passed through as given and
// validated by Dispatcher, so the CLI and server cannot drift on the limits.
func parseRawCommand(args []string, stderr io.Writer) (command, error) {
	rawFlags := flag.NewFlagSet("raw", flag.ContinueOnError)
	rawFlags.SetOutput(stderr)
	template := rawFlags.String("template", "", "template id or code (snapshots only)")
	days := rawFlags.String("days", "", "only rows from the last N days")
	since := rawFlags.String("since", "", "only rows at or after this RFC 3339 time or YYYY-MM-DD date")
	until := rawFlags.String("until", "", "only rows before this RFC 3339 time or YYYY-MM-DD date")
	limit := rawFlags.String("limit", "", "maximum rows, newest first (default 1000)")
	positional, err := parseInterspersed(rawFlags, args)
	if err != nil {
		return command{}, err
	}
	if len(positional) != 1 {
		return command{}, fmt.Errorf("raw requires one dataset: snapshots or payouts")
	}
	path, ok := rawDatasets[positional[0]]
	if !ok {
		return command{}, fmt.Errorf("unknown raw dataset %q (want snapshots or payouts)", positional[0])
	}
	if *template != "" && positional[0] != "snapshots" {
		return command{}, fmt.Errorf("--template only applies to snapshots")
	}

	query := url.Values{}
	for name, value := range map[string]string{
		"template": *template, "days": *days, "since": *since, "until": *until, "limit": *limit,
	} {
		if value != "" {
			query.Set(name, value)
		}
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return command{method: http.MethodGet, path: path, requiresAuth: true}, nil
}

// parseInterspersed lets positional arguments sit among the flags, so
// "projects my-template --days 7" works as well as "projects --days 7
// my-template"; the flag package alone stops at the first positional.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		args = flags.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func resolveCredentialsPath(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return configured, nil
	}
	return defaultCredentialsPath()
}

func resolveDispatcherURL(configured, credentialsPath string, prompt bool, input io.Reader, output io.Writer) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured, nil
	}
	stored, ok, err := loadStoredURL(credentialsPath)
	if err != nil {
		return "", err
	}
	if ok {
		return stored, nil
	}
	if !prompt {
		return defaultDispatcherURL, nil
	}

	fmt.Fprint(output, "Dispatcher URL: ")
	value, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read Dispatcher URL: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("Dispatcher URL is required (or pass --url or set DISPATCHER_URL)")
	}
	return value, nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  dispatcherctl [flags] <command>

Flags:
  --url URL          Dispatcher instance (env: DISPATCHER_URL; defaults to saved login)
  --config PATH      Credentials file (env: DISPATCHER_CONFIG)
  --compact          Emit compact JSON
  --timeout DURATION HTTP timeout (default: 1m30s)

Query commands:
  health                   Check whether the instance and database are up
  whoami                   Show the authenticated Railway user
  summary                  Show the latest analytics totals
  templates                List current template analytics
  payouts [--days N]       Show payout history (default: 30 days)
  projects TEMPLATE [--days N]
                           Show a template's total, recent and active projects
                           over time (TEMPLATE is its id or code; default: 30 days)
  notifications            List notification targets
  withdraw-settings        Show auto-withdraw settings
  withdraw-accounts        List payout destinations and balance
  get PATH                 GET any authenticated API path

Raw data:
  raw snapshots [filters]  Every collected template snapshot, all columns
  raw payouts [filters]    Every mirrored payout
    --template ID|CODE     Only this template (snapshots only)
    --days N               Only rows from the last N days
    --since T, --until T   Window bounds, RFC 3339 or YYYY-MM-DD
    --limit N              Maximum rows, newest first (default: 1000, max: 10000)

Other commands:
  login                    Authenticate through Dispatcher and Railway OAuth
  logout                   Remove the saved session for this instance
  refresh                  Collect a fresh analytics snapshot
  version                  Print the CLI version
  help                     Show this help
`)
}
