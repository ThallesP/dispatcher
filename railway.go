package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var client = &http.Client{
	Timeout: 10 * time.Second,
}

const (
	railwayRegisterURL = "https://backboard.railway.com/oauth/register"
	railwayTokenURL    = "https://backboard.railway.com/oauth/token"
	railwayGraphQLURL  = "https://backboard.railway.com/graphql/v2"
)

type clientRegistrationRequest struct {
	ClientName              string   `json:"client_name"`
	ApplicationType         string   `json:"application_type"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type clientRegistrationResponse struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func createRailwayCredentials() (RailwayCredentials, error) {
	payload, err := json.Marshal(clientRegistrationRequest{
		ClientName:              "dispatcher",
		ApplicationType:         "web",
		RedirectURIs:            []string{os.Getenv("CALLBACK_URL")},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "client_secret_basic",
	})
	if err != nil {
		return RailwayCredentials{}, err
	}

	resp, err := client.Post(railwayRegisterURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return RailwayCredentials{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return RailwayCredentials{}, fmt.Errorf("railway oauth register: %s: %s", resp.Status, body)
	}

	var reg clientRegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return RailwayCredentials{}, err
	}
	if reg.ClientID == "" || reg.ClientSecret == "" {
		return RailwayCredentials{}, fmt.Errorf("railway oauth register: response missing client credentials")
	}

	return RailwayCredentials{ClientID: reg.ClientID, ClientSecret: reg.ClientSecret}, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func exchangeAuthCode(ctx context.Context, creds RailwayCredentials, code string) (tokenResponse, error) {
	return requestToken(ctx, creds, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {os.Getenv("CALLBACK_URL")},
	})
}

func refreshAccessToken(ctx context.Context, creds RailwayCredentials) (tokenResponse, error) {
	return requestToken(ctx, creds, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {creds.RefreshToken},
	})
}

func requestToken(ctx context.Context, creds RailwayCredentials, form url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, railwayTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// client_secret_basic per RFC 6749 §2.3.1: credentials are form-encoded
	// before going into the Basic auth header.
	req.SetBasicAuth(url.QueryEscape(creds.ClientID), url.QueryEscape(creds.ClientSecret))

	resp, err := client.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return tokenResponse{}, fmt.Errorf("railway oauth token: %s: %s", resp.Status, body)
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return tokenResponse{}, err
	}
	if tok.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("railway oauth token: response missing access_token")
	}
	return tok, nil
}

// railwayQuery runs a GraphQL query against the Railway API and decodes the
// response's data payload into out.
func railwayQuery(ctx context.Context, accessToken, query string, variables map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, railwayGraphQLURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("railway graphql: %s: %s", resp.Status, body)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("railway graphql: %s", envelope.Errors[0].Message)
	}
	return json.Unmarshal(envelope.Data, out)
}

type authUser struct {
	ID         string `json:"id"`
	Avatar     string `json:"avatar"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Workspaces []struct {
		ID string `json:"id"`
	} `json:"workspaces"`
}

// getAuthUser validates the access token and returns the Railway user behind
// it. The user must belong to the workspace that owns the project this app
// is deployed in (RAILWAY_PROJECT_ID).
func getAuthUser(ctx context.Context, accessToken string) (authUser, error) {
	projectID := os.Getenv("RAILWAY_PROJECT_ID")
	var data struct {
		Project struct {
			WorkspaceID string `json:"workspaceId"`
		} `json:"project"`
		Me authUser `json:"me"`
	}
	query := "query ($id: String!) { project(id: $id) { workspaceId } me { id avatar email name workspaces { id } } }"
	if err := railwayQuery(ctx, accessToken, query, map[string]any{"id": projectID}, &data); err != nil {
		return authUser{}, err
	}
	for _, ws := range data.Me.Workspaces {
		if ws.ID != "" && ws.ID == data.Project.WorkspaceID {
			return data.Me, nil
		}
	}
	return authUser{}, fmt.Errorf("no access to the workspace owning project %s", projectID)
}

// getProjectWorkspaceID resolves the workspace that owns the project this app
// is deployed in (RAILWAY_PROJECT_ID).
func getProjectWorkspaceID(ctx context.Context, accessToken string) (string, error) {
	var data struct {
		Project struct {
			WorkspaceID string `json:"workspaceId"`
		} `json:"project"`
	}
	query := "query ($id: String!) { project(id: $id) { workspaceId } }"
	err := railwayQuery(ctx, accessToken, query, map[string]any{"id": os.Getenv("RAILWAY_PROJECT_ID")}, &data)
	if err != nil {
		return "", err
	}
	if data.Project.WorkspaceID == "" {
		return "", fmt.Errorf("project %s has no workspace", os.Getenv("RAILWAY_PROJECT_ID"))
	}
	return data.Project.WorkspaceID, nil
}

type workspaceTemplate struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Code           string   `json:"code"`
	Status         string   `json:"status"`
	Health         *float64 `json:"health"`
	Projects       int64    `json:"projects"`
	RecentProjects int64    `json:"recentProjects"`
	ActiveProjects int64    `json:"activeProjects"`
	TotalPayout    float64  `json:"totalPayout"`
}

const workspaceTemplatesQuery = `query ($workspaceId: String!) {
  workspaceTemplates(workspaceId: $workspaceId) {
    edges {
      node {
        id
        name
        code
        status
        health
        projects
        recentProjects
        activeProjects
        totalPayout
      }
    }
  }
}`

func getWorkspaceTemplates(ctx context.Context, accessToken, workspaceID string) ([]workspaceTemplate, error) {
	var data struct {
		WorkspaceTemplates struct {
			Edges []struct {
				Node workspaceTemplate `json:"node"`
			} `json:"edges"`
		} `json:"workspaceTemplates"`
	}
	err := railwayQuery(ctx, accessToken, workspaceTemplatesQuery, map[string]any{"workspaceId": workspaceID}, &data)
	if err != nil {
		return nil, err
	}
	templates := make([]workspaceTemplate, 0, len(data.WorkspaceTemplates.Edges))
	for _, edge := range data.WorkspaceTemplates.Edges {
		templates = append(templates, edge.Node)
	}
	return templates, nil
}
