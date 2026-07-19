package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"text/template"
	"time"

	"gorm.io/gorm"
)

const (
	maxNotificationTemplateSize = 8 * 1024
	healthDropThreshold         = 90.0
)

type NotificationEvent struct {
	Event      string    `json:"event"`
	Title      string    `json:"title"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurredAt"`
	Data       any       `json:"data"`
}

type PayoutNotificationData struct {
	AmountCents int64  `json:"amountCents"`
	Amount      string `json:"amount"`
	AccountID   string `json:"accountId"`
}

type HealthDropNotificationData struct {
	TemplateID   string  `json:"templateId"`
	TemplateName string  `json:"templateName"`
	Previous     float64 `json:"previous"`
	Current      float64 `json:"current"`
	Threshold    float64 `json:"threshold"`
}

type WeeklyTemplateSummary struct {
	Name           string   `json:"name"`
	NetNewProjects int64    `json:"netNewProjects"`
	PayoutDelta    float64  `json:"payoutDelta"`
	PayoutDeltaUSD string   `json:"payoutDeltaUSD"`
	Health         *float64 `json:"health"`
}

type WeeklySummaryNotificationData struct {
	From                time.Time               `json:"from"`
	To                  time.Time               `json:"to"`
	Templates           []WeeklyTemplateSummary `json:"templates"`
	TotalPayoutDelta    float64                 `json:"totalPayoutDelta"`
	TotalPayoutDeltaUSD string                  `json:"totalPayoutDeltaUSD"`
	TotalNetNewProjects int64                   `json:"totalNetNewProjects"`
	Positive            bool                    `json:"positive"`
}

type notificationPreset struct {
	Headers      map[string]string `json:"headers"`
	BodyTemplate string            `json:"bodyTemplate"`
}

var notificationPresets = map[string]notificationPreset{
	"discord": {
		Headers: map[string]string{},
		BodyTemplate: `{
  "embeds": [
    {
      "title": {{json .Title}},
      "color": {{if eq .Event "payout"}}5763719{{else if eq .Event "health_drop"}}15548997{{else if .Data.Positive}}5763719{{else}}15548997{{end}},
      "fields": [{{if eq .Event "payout"}}
        { "name": "Amount", "value": {{json .Data.Amount}}, "inline": true },
        { "name": "Account", "value": {{json .Data.AccountID}}, "inline": true }{{else if eq .Event "health_drop"}}
        { "name": "Template", "value": {{json .Data.TemplateName}}, "inline": true },
        { "name": "Health", "value": "{{printf "%.0f" .Data.Previous}}% → {{printf "%.0f" .Data.Current}}%", "inline": true },
        { "name": "Threshold", "value": "{{printf "%.0f" .Data.Threshold}}%", "inline": true }{{else}}
        { "name": "Net new projects", "value": "{{printf "%+d" .Data.TotalNetNewProjects}}", "inline": true },
        { "name": "Payout", "value": {{json .Data.TotalPayoutDeltaUSD}}, "inline": true }{{range .Data.Templates}},
        { "name": {{json .Name}}, "value": "{{printf "%+d" .NetNewProjects}} projects · {{.PayoutDeltaUSD}}{{if .Health}} · {{.Health}}% health{{end}}" }{{end}}{{end}}
      ],
      "timestamp": {{json .OccurredAt}}
    }
  ]
}`,
	},
	"slack": {
		Headers: map[string]string{},
		BodyTemplate: `{
  "attachments": [
    {
      "color": "{{if eq .Event "payout"}}good{{else if eq .Event "health_drop"}}danger{{else if .Data.Positive}}good{{else}}danger{{end}}",
      "title": {{json .Title}},
      "fallback": {{json .Title}},
      "fields": [{{if eq .Event "payout"}}
        { "title": "Amount", "value": {{json .Data.Amount}}, "short": true },
        { "title": "Account", "value": {{json .Data.AccountID}}, "short": true }{{else if eq .Event "health_drop"}}
        { "title": "Template", "value": {{json .Data.TemplateName}}, "short": true },
        { "title": "Health", "value": "{{printf "%.0f" .Data.Previous}}% → {{printf "%.0f" .Data.Current}}%", "short": true }{{else}}
        { "title": "Net new projects", "value": "{{printf "%+d" .Data.TotalNetNewProjects}}", "short": true },
        { "title": "Payout", "value": {{json .Data.TotalPayoutDeltaUSD}}, "short": true }{{range .Data.Templates}},
        { "title": {{json .Name}}, "value": "{{printf "%+d" .NetNewProjects}} projects · {{.PayoutDeltaUSD}}{{if .Health}} · {{.Health}}% health{{end}}" }{{end}}{{end}}
      ]
    }
  ]
}`,
	},
	"ntfy": {
		Headers: map[string]string{
			"X-Title":      "{{.Title}}",
			"X-Tags":       `{{if eq .Event "payout"}}moneybag{{else if eq .Event "health_drop"}}rotating_light{{else if .Data.Positive}}chart_with_upwards_trend{{else}}chart_with_downwards_trend{{end}}`,
			"X-Priority":   `{{if eq .Event "health_drop"}}4{{else}}3{{end}}`,
			"Content-Type": "text/plain",
		},
		BodyTemplate: "{{.Message}}",
	},
	"custom": {
		Headers:      map[string]string{},
		BodyTemplate: "",
	},
}

var notificationTemplateFuncs = template.FuncMap{
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	},
}

var webhookClient = &http.Client{Timeout: 10 * time.Second}

type notificationStatusError struct {
	StatusCode int
}

func (e *notificationStatusError) Error() string {
	return fmt.Sprintf("status %d", e.StatusCode)
}

func notificationHeaders(target NotificationTarget) (map[string]string, error) {
	headers := map[string]string{}
	if target.HeadersJSON == "" {
		return headers, nil
	}
	if err := json.Unmarshal([]byte(target.HeadersJSON), &headers); err != nil {
		return nil, fmt.Errorf("invalid headers: %w", err)
	}
	if headers == nil {
		headers = map[string]string{}
	}
	return headers, nil
}

func encodeNotificationHeaders(headers map[string]string) (string, error) {
	if headers == nil {
		headers = map[string]string{}
	}
	b, err := json.Marshal(headers)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func renderNotificationTemplate(name, source string, ev NotificationEvent) (string, error) {
	t, err := parseNotificationTemplate(name, source)
	if err != nil {
		return "", err
	}
	var rendered bytes.Buffer
	if err := t.Execute(&rendered, ev); err != nil {
		return "", err
	}
	return rendered.String(), nil
}

func parseNotificationTemplate(name, source string) (*template.Template, error) {
	return template.New(name).
		Funcs(notificationTemplateFuncs).
		Option("missingkey=error").
		Parse(source)
}

func renderNotification(target NotificationTarget, ev NotificationEvent) ([]byte, http.Header, error) {
	body, err := renderNotificationTemplate("body", target.BodyTemplate, ev)
	if err != nil {
		return nil, nil, fmt.Errorf("body template: %w", err)
	}
	rawHeaders, err := notificationHeaders(target)
	if err != nil {
		return nil, nil, err
	}
	headers := make(http.Header, len(rawHeaders)+1)
	seen := map[string]bool{}
	for name, source := range rawHeaders {
		if strings.ContainsAny(name, "\r\n") {
			return nil, nil, fmt.Errorf("header name contains a newline")
		}
		canonical := http.CanonicalHeaderKey(name)
		lower := strings.ToLower(canonical)
		if seen[lower] {
			return nil, nil, fmt.Errorf("duplicate header %q", canonical)
		}
		seen[lower] = true
		if prohibitedNotificationHeader(lower) {
			return nil, nil, fmt.Errorf("header %q is not allowed", canonical)
		}
		value, err := renderNotificationTemplate("header "+name, source, ev)
		if err != nil {
			return nil, nil, fmt.Errorf("header %q: %w", name, err)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, nil, fmt.Errorf("header %q contains a newline", name)
		}
		headers.Set(canonical, value)
	}
	if headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "application/json")
	}
	return []byte(body), headers, nil
}

func prohibitedNotificationHeader(lower string) bool {
	switch lower {
	case "host", "content-length", "transfer-encoding", "connection":
		return true
	default:
		return false
	}
}

func notificationContentTypeIsJSON(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func validateNotificationTarget(target NotificationTarget) error {
	if strings.TrimSpace(target.Name) == "" {
		return errors.New("name is required")
	}
	if _, ok := notificationPresets[target.Kind]; !ok {
		return errors.New("kind must be discord, slack, ntfy, or custom")
	}
	parsedURL, err := url.Parse(target.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return errors.New("url must be an http or https URL with a host")
	}
	if parsedURL.User != nil {
		return errors.New("url must not contain user credentials")
	}
	if len(target.BodyTemplate) > maxNotificationTemplateSize {
		return fmt.Errorf("bodyTemplate must be at most %d bytes", maxNotificationTemplateSize)
	}
	if target.BodyTemplate == "" {
		return errors.New("bodyTemplate is required")
	}
	if _, err := parseNotificationTemplate("body", target.BodyTemplate); err != nil {
		return fmt.Errorf("body template: %w", err)
	}
	rawHeaders, err := notificationHeaders(target)
	if err != nil {
		return err
	}
	for name, value := range rawHeaders {
		if strings.ContainsAny(name, "\r\n") {
			return errors.New("header name contains a newline")
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("header %q contains a newline", name)
		}
		if prohibitedNotificationHeader(strings.ToLower(http.CanonicalHeaderKey(name))) {
			return fmt.Errorf("header %q is not allowed", name)
		}
		if _, err := parseNotificationTemplate("header "+name, value); err != nil {
			return fmt.Errorf("header %q: %w", name, err)
		}
	}

	events := subscribedSampleEvents(target)
	for _, ev := range events {
		body, headers, err := renderNotification(target, ev)
		if err != nil {
			return err
		}
		if notificationContentTypeIsJSON(headers.Get("Content-Type")) && !json.Valid(body) {
			return fmt.Errorf("body template renders invalid JSON for %s", ev.Event)
		}
	}
	return nil
}

func subscribedSampleEvents(target NotificationTarget) []NotificationEvent {
	events := []NotificationEvent{}
	if target.OnPayout {
		events = append(events, sampleNotificationEvent("payout"))
	}
	if target.OnHealthDrop {
		events = append(events, sampleNotificationEvent("health_drop"))
	}
	if target.OnWeeklySummary {
		events = append(events, sampleNotificationEvent("weekly_summary"))
	}
	return events
}

func sampleNotificationEvent(event string) NotificationEvent {
	at := time.Date(2026, time.July, 18, 9, 0, 0, 0, time.UTC)
	switch event {
	case "payout":
		return NotificationEvent{
			Event:      event,
			Title:      "Withdrawal requested",
			Message:    "Requested a $123.45 withdrawal from your Railway balance.",
			OccurredAt: at,
			Data: PayoutNotificationData{
				AmountCents: 12345,
				Amount:      "$123.45",
				AccountID:   "account_example",
			},
		}
	case "health_drop":
		return NotificationEvent{
			Event:      event,
			Title:      "Template health dropped",
			Message:    "Example template health dropped from 96% to 87% (below 90%).",
			OccurredAt: at,
			Data: HealthDropNotificationData{
				TemplateID:   "template_example",
				TemplateName: "Example template",
				Previous:     96,
				Current:      87,
				Threshold:    healthDropThreshold,
			},
		}
	case "weekly_summary":
		health := 98.0
		data := WeeklySummaryNotificationData{
			From: at.AddDate(0, 0, -7),
			To:   at,
			Templates: []WeeklyTemplateSummary{{
				Name:           "Example template",
				NetNewProjects: 12,
				PayoutDelta:    45.67,
				Health:         &health,
			}},
			TotalPayoutDelta:    45.67,
			TotalNetNewProjects: 12,
		}
		return weeklySummaryEvent(data, at)
	default:
		return NotificationEvent{
			Event:      "test",
			Title:      "Test notification",
			Message:    "Your Dispatcher notification target is working.",
			OccurredAt: at,
			Data:       struct{}{},
		}
	}
}

func sendNotification(ctx context.Context, target NotificationTarget, ev NotificationEvent) error {
	body, headers, err := renderNotification(target, ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return errors.New("could not create request")
	}
	req.Header = headers
	resp, err := webhookClient.Do(req)
	if err != nil {
		return errors.New("request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &notificationStatusError{StatusCode: resp.StatusCode}
	}
	return nil
}

// notifyAll sends an event to every enabled target subscribed to it. Delivery
// is intentionally best-effort: failures are logged and never retried.
func notifyAll(db *gorm.DB, ev NotificationEvent) {
	query := gorm.G[NotificationTarget](db).Where("enabled = ?", true)
	switch ev.Event {
	case "payout":
		query = query.Where("on_payout = ?", true)
	case "health_drop":
		query = query.Where("on_health_drop = ?", true)
	case "weekly_summary":
		query = query.Where("on_weekly_summary = ?", true)
	default:
		return
	}
	targets, err := query.Order("created_at").Find(context.Background())
	if err != nil {
		log.Printf("notify: load targets: %v", err)
		return
	}
	for _, target := range targets {
		if err := sendNotification(context.Background(), target, ev); err != nil {
			log.Printf("notify: %s: %v", target.Name, err)
		}
	}
}

func crossedHealthThreshold(previous, current *float64) bool {
	return previous != nil && current != nil && *previous >= healthDropThreshold && *current < healthDropThreshold
}

func healthDropEvent(current TemplateSnapshot, previous float64) NotificationEvent {
	return NotificationEvent{
		Event: "health_drop",
		Title: "Template health dropped",
		Message: fmt.Sprintf("%s health dropped from %.0f%% to %.0f%% (below %.0f%%).",
			current.Name, previous, *current.Health, healthDropThreshold),
		OccurredAt: current.SampledAt,
		Data: HealthDropNotificationData{
			TemplateID:   current.TemplateID,
			TemplateName: current.Name,
			Previous:     previous,
			Current:      *current.Health,
			Threshold:    healthDropThreshold,
		},
	}
}

func aggregateWeeklySnapshots(snapshots []TemplateSnapshot, from, to time.Time) (WeeklySummaryNotificationData, bool) {
	byTemplate := map[string][]TemplateSnapshot{}
	for _, snapshot := range snapshots {
		if snapshot.SampledAt.After(to) {
			continue
		}
		byTemplate[snapshot.TemplateID] = append(byTemplate[snapshot.TemplateID], snapshot)
	}
	if len(byTemplate) == 0 {
		return WeeklySummaryNotificationData{}, false
	}

	data := WeeklySummaryNotificationData{
		From:      from,
		To:        to,
		Templates: []WeeklyTemplateSummary{},
	}
	for _, samples := range byTemplate {
		sort.SliceStable(samples, func(i, j int) bool {
			if samples[i].SampledAt.Equal(samples[j].SampledAt) {
				return samples[i].ID < samples[j].ID
			}
			return samples[i].SampledAt.Before(samples[j].SampledAt)
		})
		current := samples[len(samples)-1]
		var baseline *TemplateSnapshot
		for i := range samples {
			if !samples[i].SampledAt.After(from) {
				baseline = &samples[i]
			}
		}
		if baseline == nil {
			for i := range samples {
				if !samples[i].SampledAt.Before(from) {
					baseline = &samples[i]
					break
				}
			}
		}
		if baseline == nil {
			continue
		}
		health := cloneFloat64(current.Health)
		templateSummary := WeeklyTemplateSummary{
			Name:           current.Name,
			NetNewProjects: current.Projects - baseline.Projects,
			PayoutDelta:    current.TotalPayout - baseline.TotalPayout,
			Health:         health,
		}
		data.Templates = append(data.Templates, templateSummary)
		data.TotalNetNewProjects += templateSummary.NetNewProjects
		data.TotalPayoutDelta += templateSummary.PayoutDelta
	}
	sort.Slice(data.Templates, func(i, j int) bool {
		return strings.ToLower(data.Templates[i].Name) < strings.ToLower(data.Templates[j].Name)
	})
	return data, len(data.Templates) > 0
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func weeklySummaryEvent(data WeeklySummaryNotificationData, occurredAt time.Time) NotificationEvent {
	data.Positive = data.TotalPayoutDelta >= 0
	data.TotalPayoutDeltaUSD = signedUSD(data.TotalPayoutDelta)
	for i := range data.Templates {
		data.Templates[i].PayoutDeltaUSD = signedUSD(data.Templates[i].PayoutDelta)
	}
	var message strings.Builder
	message.WriteString("Weekly template summary")
	message.WriteString(fmt.Sprintf("\n%+d net new projects · %s payout", data.TotalNetNewProjects, data.TotalPayoutDeltaUSD))
	for _, item := range data.Templates {
		message.WriteString(fmt.Sprintf("\n%s: %+d projects · %s payout", item.Name, item.NetNewProjects, item.PayoutDeltaUSD))
	}
	return NotificationEvent{
		Event:      "weekly_summary",
		Title:      "Weekly template summary",
		Message:    message.String(),
		OccurredAt: occurredAt,
		Data:       data,
	}
}

func signedUSD(amount float64) string {
	if amount < 0 {
		return fmt.Sprintf("-$%.2f", -amount)
	}
	return fmt.Sprintf("+$%.2f", amount)
}

func loadWeeklySummary(db *gorm.DB, now time.Time) (NotificationEvent, bool, error) {
	from := now.AddDate(0, 0, -7)
	snapshots := []TemplateSnapshot{}
	err := db.Raw(`
		SELECT * FROM template_snapshots
		WHERE sampled_at >= ? AND sampled_at <= ?
		UNION ALL
		SELECT baseline.*
		FROM template_snapshots baseline
		JOIN (
			SELECT template_id, MAX(sampled_at) AS sampled_at
			FROM template_snapshots
			WHERE sampled_at < ?
			GROUP BY template_id
		) previous
		  ON previous.template_id = baseline.template_id
		 AND previous.sampled_at = baseline.sampled_at
		ORDER BY sampled_at, id`, from, now, from).Scan(&snapshots).Error
	if err != nil {
		return NotificationEvent{}, false, err
	}
	data, ok := aggregateWeeklySnapshots(snapshots, from, now)
	if !ok {
		return NotificationEvent{}, false, nil
	}
	return weeklySummaryEvent(data, now), true, nil
}
