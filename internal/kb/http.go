package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// UserAgent is sent on every request. Anthropic's usage guide asks
// integrations to set one.
var UserAgent = "keyburn (https://github.com/sudodrift/keyburn)"

type api struct {
	base    string
	headers map[string]string
	secret  string // only used to scrub error text
	client  *http.Client
}

func newAPI(base string, headers map[string]string, secret string) *api {
	return &api{base: strings.TrimRight(base, "/"), headers: headers, secret: secret,
		client: &http.Client{Timeout: 60 * time.Second}}
}

// get fetches base+path and decodes the JSON body into out.
func (a *api) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return err
	}
	for k, v := range a.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		// net/http errors hold the URL, never the headers.
		return fmt.Errorf("%s", redact(err.Error(), a.secret))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		return fmt.Errorf("GET %s: HTTP %d: %s", pathOnly(path), resp.StatusCode, redact(snippet, a.secret))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: bad JSON: %v", pathOnly(path), err)
	}
	return nil
}

func pathOnly(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

// maxPages stops a server that always says has_more from looping forever.
const maxPages = 100
