package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// Notifier sends new alerts somewhere other than stdout.
type Notifier interface {
	Name() string
	Send(ctx context.Context, alerts []Alert) error
}

func summary(alerts []Alert) (title, body string) {
	title = fmt.Sprintf("keyburn: %d new alert", len(alerts))
	if len(alerts) != 1 {
		title += "s"
	}
	var b strings.Builder
	for _, a := range alerts {
		b.WriteString(a.Message)
		b.WriteByte('\n')
	}
	return title, b.String()
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func post(ctx context.Context, u string, body []byte, headers map[string]string, secret string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s", redact(err.Error(), secret))
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// NtfyNotifier publishes to an ntfy topic: POST the message body to
// <server>/<topic>, with the title, priority and tags as headers.
type NtfyNotifier struct {
	Server, Topic, Token, Priority string
}

func (n NtfyNotifier) Name() string { return "ntfy" }

func (n NtfyNotifier) Send(ctx context.Context, alerts []Alert) error {
	title, body := summary(alerts)
	h := map[string]string{"Title": title, "Tags": "rotating_light", "Content-Type": "text/plain; charset=utf-8"}
	p := n.Priority
	if p == "" {
		p = "high"
	}
	h["Priority"] = p
	if n.Token != "" {
		h["Authorization"] = "Bearer " + n.Token
	}
	u := strings.TrimRight(n.Server, "/") + "/" + url.PathEscape(n.Topic)
	return post(ctx, u, []byte(body), h, n.Token)
}

// WebhookNotifier POSTs a JSON document with every new alert.
type WebhookNotifier struct {
	URL, Bearer string
}

func (w WebhookNotifier) Name() string { return "webhook" }

// WebhookPayload is the JSON body sent to the webhook.
type WebhookPayload struct {
	Tool   string  `json:"tool"`
	Title  string  `json:"title"`
	Text   string  `json:"text"`
	Alerts []Alert `json:"alerts"`
}

func (w WebhookNotifier) Send(ctx context.Context, alerts []Alert) error {
	title, body := summary(alerts)
	data, err := json.Marshal(WebhookPayload{Tool: "keyburn", Title: title, Text: body, Alerts: alerts})
	if err != nil {
		return err
	}
	h := map[string]string{"Content-Type": "application/json"}
	if w.Bearer != "" {
		h["Authorization"] = "Bearer " + w.Bearer
	}
	return post(ctx, w.URL, data, h, w.Bearer)
}

// SendmailNotifier pipes a plain text mail to sendmail -t -i: -t reads the
// recipients from the To header, -i keeps a line with a single dot from
// ending the message (flags as documented in Postfix's sendmail(1)).
type SendmailNotifier struct {
	Path, From string
	To         []string
}

func (s SendmailNotifier) Name() string { return "sendmail" }

func (s SendmailNotifier) Send(ctx context.Context, alerts []Alert) error {
	title, body := summary(alerts)
	var b strings.Builder
	if s.From != "" {
		fmt.Fprintf(&b, "From: %s\n", headerSafe(s.From))
	}
	to := make([]string, len(s.To))
	for i, t := range s.To {
		to[i] = headerSafe(t)
	}
	fmt.Fprintf(&b, "To: %s\nSubject: %s\nContent-Type: text/plain; charset=utf-8\n\n%s", strings.Join(to, ", "), title, body)
	cmd := exec.CommandContext(ctx, s.Path, "-t", "-i")
	cmd.Stdin = strings.NewReader(b.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", s.Path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// headerSafe stops a config value from adding mail headers.
func headerSafe(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// Notifiers builds the configured channels, loading their tokens.
func Notifiers(n Notify, env func(string) string) ([]Notifier, error) {
	var out []Notifier
	if n.Ntfy != nil {
		tok := ""
		if n.Ntfy.TokenEnv != "" || n.Ntfy.TokenFile != "" {
			t, err := LoadSecret(env, n.Ntfy.TokenEnv, n.Ntfy.TokenFile)
			if err != nil {
				return nil, fmt.Errorf("ntfy token: %v", err)
			}
			tok = t
		}
		out = append(out, NtfyNotifier{Server: n.Ntfy.Server, Topic: n.Ntfy.Topic, Token: tok, Priority: n.Ntfy.Priority})
	}
	if n.Webhook != nil {
		tok := ""
		if n.Webhook.BearerEnv != "" || n.Webhook.BearerFile != "" {
			t, err := LoadSecret(env, n.Webhook.BearerEnv, n.Webhook.BearerFile)
			if err != nil {
				return nil, fmt.Errorf("webhook token: %v", err)
			}
			tok = t
		}
		out = append(out, WebhookNotifier{URL: n.Webhook.URL, Bearer: tok})
	}
	if n.Sendmail != nil {
		out = append(out, SendmailNotifier{Path: n.Sendmail.Path, From: n.Sendmail.From, To: n.Sendmail.To})
	}
	return out, nil
}
