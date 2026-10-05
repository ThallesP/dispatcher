package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const loginTimeout = 10 * time.Minute

type storedSession struct {
	Session   string    `json:"session"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type credentialsFile struct {
	URL      string                   `json:"url,omitempty"`
	Sessions map[string]storedSession `json:"sessions"`
}

func defaultCredentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	return filepath.Join(dir, "dispatcher", "credentials.json"), nil
}

func loadStoredSession(path, instance string, now time.Time) (storedSession, bool, error) {
	credentials, err := readCredentials(path)
	if err != nil {
		return storedSession{}, false, err
	}
	session, ok := credentials.Sessions[instance]
	if !ok || session.Session == "" || !session.ExpiresAt.After(now) {
		return storedSession{}, false, nil
	}
	return session, true, nil
}

func loadStoredURL(path string) (string, bool, error) {
	credentials, err := readCredentials(path)
	if err != nil {
		return "", false, err
	}
	if instance := strings.TrimSpace(credentials.URL); instance != "" {
		return instance, true, nil
	}
	// Older credentials files did not have URL. Preserve the obvious default
	// when they contain a session for exactly one Dispatcher instance.
	if len(credentials.Sessions) == 1 {
		for instance := range credentials.Sessions {
			return instance, true, nil
		}
	}
	return "", false, nil
}

func saveStoredSession(path, instance string, session storedSession) error {
	credentials, err := readCredentials(path)
	if err != nil {
		return err
	}
	if credentials.Sessions == nil {
		credentials.Sessions = make(map[string]storedSession)
	}
	credentials.URL = instance
	credentials.Sessions[instance] = session
	return writeCredentials(path, credentials)
}

func removeStoredSession(path, instance string) error {
	credentials, err := readCredentials(path)
	if err != nil {
		return err
	}
	delete(credentials.Sessions, instance)
	return writeCredentials(path, credentials)
}

func readCredentials(path string) (credentialsFile, error) {
	credentials := credentialsFile{Sessions: make(map[string]storedSession)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return credentials, nil
	}
	if err != nil {
		return credentialsFile{}, fmt.Errorf("read credentials: %w", err)
	}
	if err := json.Unmarshal(data, &credentials); err != nil {
		return credentialsFile{}, fmt.Errorf("decode credentials: %w", err)
	}
	if credentials.Sessions == nil {
		credentials.Sessions = make(map[string]storedSession)
	}
	return credentials, nil
}

func writeCredentials(path string, credentials credentialsFile) error {
	data, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create credentials directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open credentials: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect credentials: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close credentials: %w", err)
	}
	return nil
}

func login(ctx context.Context, client *apiClient, credentialsPath string, output io.Writer, openURL func(string) error) error {
	if err := requireSecureLoginURL(client.baseURL); err != nil {
		return err
	}
	verifier, err := randomSecret()
	if err != nil {
		return err
	}
	startPayload, err := json.Marshal(map[string]string{"challenge": loginChallenge(verifier)})
	if err != nil {
		return err
	}
	startBody, err := client.requestBody(ctx, http.MethodPost, "/api/auth/cli/start", bytes.NewReader(startPayload), "application/json")
	if err != nil {
		return err
	}
	var start struct {
		AuthorizationURL string    `json:"authorizationUrl"`
		Code             string    `json:"code"`
		ExpiresAt        time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(startBody, &start); err != nil {
		return fmt.Errorf("decode login start: %w", err)
	}
	if !validEncodedSecret(start.Code) || !start.ExpiresAt.After(time.Now()) {
		return errors.New("Dispatcher returned an invalid CLI login")
	}
	authorizationURL, err := validateRailwayAuthorizationURL(start.AuthorizationURL)
	if err != nil {
		return err
	}

	fmt.Fprintf(output, "Open this URL to log in with Railway:\n%s\n", authorizationURL)
	if err := openURL(authorizationURL); err != nil {
		fmt.Fprintln(output, "The browser could not be opened automatically; use the URL above.")
	}

	exchangePayload, err := json.Marshal(map[string]string{"code": start.Code, "verifier": verifier})
	if err != nil {
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		body, err := client.requestBody(ctx, http.MethodPost, "/api/auth/cli/exchange", bytes.NewReader(exchangePayload), "application/json")
		if err != nil {
			return err
		}
		var exchange struct {
			Status string `json:"status"`
			storedSession
		}
		if err := json.Unmarshal(body, &exchange); err != nil {
			return fmt.Errorf("decode login response: %w", err)
		}
		if exchange.Session != "" {
			if !exchange.ExpiresAt.After(time.Now()) {
				return errors.New("Dispatcher returned an expired session")
			}
			if err := saveStoredSession(credentialsPath, client.baseURL, exchange.storedSession); err != nil {
				return err
			}
			fmt.Fprintf(output, "Logged in to %s.\n", client.baseURL)
			return nil
		}
		if exchange.Status != "pending" {
			return errors.New("Dispatcher returned an invalid login status")
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("login timed out: %w", ctx.Err())
		}
	}
}

// loginWithToken redeems a one-time token minted from the Dispatcher
// dashboard. The browser already went through Railway's consent when the
// token was minted, so there is nothing to open or poll.
func loginWithToken(ctx context.Context, client *apiClient, credentialsPath, token string, output io.Writer) error {
	if err := requireSecureLoginURL(client.baseURL); err != nil {
		return err
	}
	if !validEncodedSecret(token) {
		return errors.New("invalid login token; copy the command from the dashboard again")
	}
	payload, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return err
	}
	body, err := client.requestBody(ctx, http.MethodPost, "/api/auth/cli/redeem", bytes.NewReader(payload), "application/json")
	if err != nil {
		return err
	}
	var session storedSession
	if err := json.Unmarshal(body, &session); err != nil {
		return fmt.Errorf("decode login response: %w", err)
	}
	if session.Session == "" || !session.ExpiresAt.After(time.Now()) {
		return errors.New("Dispatcher returned an invalid session")
	}
	if err := saveStoredSession(credentialsPath, client.baseURL, session); err != nil {
		return err
	}
	fmt.Fprintf(output, "Logged in to %s.\n", client.baseURL)
	return nil
}

func requireSecureLoginURL(instance string) error {
	u, err := url.Parse(instance)
	if err != nil {
		return errors.New("invalid Dispatcher URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("CLI login requires HTTPS except for a local Dispatcher instance")
}

func openBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return command.Start()
}

func randomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate login state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func loginChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func validEncodedSecret(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validateRailwayAuthorizationURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "backboard.railway.com") ||
		u.User != nil || u.Fragment != "" || u.Path != "/oauth/auth" ||
		u.Query().Get("client_id") == "" || u.Query().Get("state") == "" {
		return "", errors.New("Dispatcher returned an invalid Railway authorization URL")
	}
	return u.String(), nil
}
