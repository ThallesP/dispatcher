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
	// Withdrawal/earnings resolvers live on Railway's internal GraphQL
	// endpoints, not the public /graphql/v2. The `customer` field on a
	// workspace is only exposed on /graphql/v2/internal.
	railwayGraphQLV2InternalURL = "https://backboard.railway.com/graphql/v2/internal"
	railwayGraphQLInternalURL   = "https://backboard.railway.com/graphql/internal"
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

// graphqlRequest posts a GraphQL query to the given Railway endpoint and
// decodes the data payload into out.
func graphqlRequest(ctx context.Context, endpoint, accessToken, query string, variables map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
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
	if err := graphqlRequest(ctx, railwayGraphQLURL, accessToken, query, map[string]any{"id": projectID}, &data); err != nil {
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
	err := graphqlRequest(ctx, railwayGraphQLURL, accessToken, query, map[string]any{"id": os.Getenv("RAILWAY_PROJECT_ID")}, &data)
	if err != nil {
		return "", err
	}
	if data.Project.WorkspaceID == "" {
		return "", fmt.Errorf("project %s has no workspace", os.Getenv("RAILWAY_PROJECT_ID"))
	}
	return data.Project.WorkspaceID, nil
}

type workspaceTemplate struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Code   string `json:"code"`
	Status string `json:"status"`
}

const workspaceTemplatesQuery = `query ($workspaceId: String!) {
  workspaceTemplates(workspaceId: $workspaceId) {
    edges {
      node {
        id
        name
        code
        status
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
	err := graphqlRequest(ctx, railwayGraphQLURL, accessToken, workspaceTemplatesQuery, map[string]any{"workspaceId": workspaceID}, &data)
	if err != nil {
		return nil, err
	}
	templates := make([]workspaceTemplate, 0, len(data.WorkspaceTemplates.Edges))
	for _, edge := range data.WorkspaceTemplates.Edges {
		templates = append(templates, edge.Node)
	}
	return templates, nil
}

// templateMetrics is the complete TemplateMetrics response currently used by
// Railway's dashboard. Keep every field here even when the Dispatcher UI does
// not consume it; snapshots persist the whole response for future reporting.
type templateMetrics struct {
	TotalDeployments        int64   `json:"totalDeployments"`
	ActiveDeployments       int64   `json:"activeDeployments"`
	DeploymentsLast90Days   int64   `json:"deploymentsLast90Days"`
	TotalEarnings           float64 `json:"totalEarnings"`
	EarningsLast90Days      float64 `json:"earningsLast90Days"`
	EarningsLast30Days      float64 `json:"earningsLast30Days"`
	TemplateHealth          float64 `json:"templateHealth"`
	SupportHealth           float64 `json:"supportHealth"`
	EligibleForSupportBonus bool    `json:"eligibleForSupportBonus"`
}

const templateMetricsQuery = `query templateMetrics($id: String!) {
  templateMetrics(id: $id) {
      totalDeployments
      activeDeployments
      deploymentsLast90Days
      totalEarnings
      earningsLast90Days
      earningsLast30Days
      templateHealth
      supportHealth
      eligibleForSupportBonus
  }
}`

// getTemplateMetrics loads the complete metrics payload for each published
// template. Railway exposes only templateMetrics(id: String!), not a list/ids
// resolver, and rejects unpublished templates, so callers pass only IDs whose
// fresh workspaceTemplates status is PUBLISHED.
func getTemplateMetrics(ctx context.Context, accessToken string, templateIDs []string) (map[string]templateMetrics, error) {
	metricsByTemplate := make(map[string]templateMetrics, len(templateIDs))
	endpoint := railwayGraphQLInternalURL + "?q=templateMetrics"
	for _, templateID := range templateIDs {
		var data struct {
			Metrics templateMetrics `json:"templateMetrics"`
		}
		if err := graphqlRequest(ctx, endpoint, accessToken, templateMetricsQuery,
			map[string]any{"id": templateID}, &data); err != nil {
			return nil, fmt.Errorf("template metrics for %s: %w", templateID, err)
		}
		metricsByTemplate[templateID] = data.Metrics
	}
	return metricsByTemplate, nil
}

// withdrawMinimumCents is Railway's floor for a cash withdrawal ($100). The
// backboard API rejects anything below it ("You cannot withdraw less than 100
// dollars.").
const withdrawMinimumCents int64 = 10000

// getWorkspaceCustomerID resolves the billing customer id for a workspace,
// needed to read balances and request cash withdrawals. The `customer` field
// is only exposed on the internal endpoint.
func getWorkspaceCustomerID(ctx context.Context, accessToken, workspaceID string) (string, error) {
	var data struct {
		Me struct {
			Workspaces []struct {
				ID       string `json:"id"`
				Customer struct {
					ID string `json:"id"`
				} `json:"customer"`
			} `json:"workspaces"`
		} `json:"me"`
	}
	query := "query { me { workspaces { id customer { id } } } }"
	if err := graphqlRequest(ctx, railwayGraphQLV2InternalURL, accessToken, query, nil, &data); err != nil {
		return "", err
	}
	for _, ws := range data.Me.Workspaces {
		if ws.ID == workspaceID {
			if ws.Customer.ID == "" {
				return "", fmt.Errorf("workspace %s has no billing customer", workspaceID)
			}
			return ws.Customer.ID, nil
		}
	}
	return "", fmt.Errorf("workspace %s not found for user", workspaceID)
}

// withdrawalAccount is a payout destination (a Stripe Connect bank/card) the
// customer can withdraw cash to.
type withdrawalAccount struct {
	ID            string `json:"id"`
	Platform      string `json:"platform"`
	StripeConnect struct {
		HasOnboarded   bool   `json:"hasOnboarded"`
		NeedsAttention bool   `json:"needsAttention"`
		BankLast4      string `json:"bankLast4"`
		CardLast4      string `json:"cardLast4"`
	} `json:"stripeConnectInfo"`
}

const withdrawalAccountsQuery = `query ($customerId: String!) {
  withdrawalAccountsV2(customerId: $customerId) {
    id
    platform
    stripeConnectInfo {
      hasOnboarded
      needsAttention
      bankLast4
      cardLast4
    }
  }
}`

func getWithdrawalAccounts(ctx context.Context, accessToken, customerID string) ([]withdrawalAccount, error) {
	var data struct {
		Accounts []withdrawalAccount `json:"withdrawalAccountsV2"`
	}
	err := graphqlRequest(ctx, railwayGraphQLInternalURL, accessToken, withdrawalAccountsQuery,
		map[string]any{"customerId": customerID}, &data)
	if err != nil {
		return nil, err
	}
	return data.Accounts, nil
}

// getAvailableBalance returns the customer's withdrawable balance in cents.
func getAvailableBalance(ctx context.Context, accessToken, customerID string) (int64, error) {
	var data struct {
		Balance int64 `json:"withdrawalAvailableBalance"`
	}
	query := "query ($customerId: String!) { withdrawalAvailableBalance(customerId: $customerId) }"
	err := graphqlRequest(ctx, railwayGraphQLInternalURL, accessToken, query,
		map[string]any{"customerId": customerID}, &data)
	return data.Balance, err
}

// getPendingWithdrawalCount reports how many withdrawals are still PENDING, so
// the auto-withdraw job never stacks a second request on an unsettled one.
func getPendingWithdrawalCount(ctx context.Context, accessToken, customerID string) (int, error) {
	var data struct {
		Withdrawals []struct {
			ID string `json:"id"`
		} `json:"withdrawals"`
	}
	query := "query ($customerId: String!, $status: WithdrawalStatusType) { withdrawals(customerId: $customerId, status: $status) { id } }"
	err := graphqlRequest(ctx, railwayGraphQLInternalURL, accessToken, query,
		map[string]any{"customerId": customerID, "status": "PENDING"}, &data)
	return len(data.Withdrawals), err
}

// createCashWithdrawal requests a cash payout of amountCents to the given
// account. The mutation returns a bare success boolean. This moves real money.
func createCashWithdrawal(ctx context.Context, accessToken, customerID, accountID string, amountCents int64) error {
	var data struct {
		OK bool `json:"withdrawalToCashCreate"`
	}
	query := "mutation ($input: WithdrawalRequestInput!) { withdrawalToCashCreate(input: $input) }"
	input := map[string]any{
		"customerId":          customerID,
		"amount":              amountCents,
		"withdrawalAccountId": accountID,
	}
	if err := graphqlRequest(ctx, railwayGraphQLInternalURL, accessToken, query,
		map[string]any{"input": input}, &data); err != nil {
		return err
	}
	if !data.OK {
		return fmt.Errorf("withdrawalToCashCreate returned false")
	}
	return nil
}

// unifiedWithdrawalsQuery is Railway's payout history — the same query their
// billing dashboard issues. The connection is a union: cash payouts
// (Withdrawal) carry an id, a status and the destination account, while credit
// payouts (CreditWithdrawalInfo) expose only an amount and a date. Newest
// first.
const unifiedWithdrawalsQuery = `query unifiedWithdrawalsV2($first: Int, $after: String, $customerId: String!) {
  unifiedWithdrawalsV2(first: $first, after: $after, customerId: $customerId) {
    pageInfo {
      hasNextPage
      endCursor
    }
    edges {
      node {
        __typename
        ... on Withdrawal {
          id
          amount
          status
          createdAt
          withdrawalAccount {
            id
            platform
            stripeConnectInfo {
              bankLast4
              cardLast4
            }
          }
        }
        ... on CreditWithdrawalInfo {
          amount
          createdAt
        }
      }
    }
  }
}`

// payoutRecord is one entry of Railway's payout history. Typename
// discriminates the union arm, so the cash-only fields (ID, Status, Account)
// are zero on a credit payout.
type payoutRecord struct {
	Typename  string    `json:"__typename"`
	ID        string    `json:"id"`
	Amount    int64     `json:"amount"` // cents
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	Account   struct {
		ID            string `json:"id"`
		Platform      string `json:"platform"`
		StripeConnect struct {
			BankLast4 string `json:"bankLast4"`
			CardLast4 string `json:"cardLast4"`
		} `json:"stripeConnectInfo"`
	} `json:"withdrawalAccount"`
}

const (
	payoutPageSize = 100
	// maxPayoutPages bounds the cursor walk so a pathological history can
	// never spin a request forever. 100 × 20 = 2,000 payouts, several years of
	// daily withdrawals.
	maxPayoutPages = 20
)

// getPayoutHistory walks the whole payout connection, newest first. Railway
// exposes no aggregate resolver, so charting monthly totals means reading
// every page. truncated reports that the walk hit maxPayoutPages before the
// end of the history.
func getPayoutHistory(ctx context.Context, accessToken, customerID string) (records []payoutRecord, truncated bool, err error) {
	endpoint := railwayGraphQLInternalURL + "?q=unifiedWithdrawalsV2"
	after := ""
	for range maxPayoutPages {
		var data struct {
			Connection struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Edges []struct {
					Node payoutRecord `json:"node"`
				} `json:"edges"`
			} `json:"unifiedWithdrawalsV2"`
		}
		variables := map[string]any{"first": payoutPageSize, "after": after, "customerId": customerID}
		if err := graphqlRequest(ctx, endpoint, accessToken, unifiedWithdrawalsQuery, variables, &data); err != nil {
			return nil, false, err
		}
		for _, edge := range data.Connection.Edges {
			records = append(records, edge.Node)
		}
		// An empty cursor with more pages claimed would loop forever on the
		// same page, so treat it as the end of the history.
		if !data.Connection.PageInfo.HasNextPage || data.Connection.PageInfo.EndCursor == "" {
			return records, false, nil
		}
		after = data.Connection.PageInfo.EndCursor
	}
	return records, true, nil
}
