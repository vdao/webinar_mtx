// Package mtx is a minimal client for the MediaMTX REST API.
package mtx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client talks to the MediaMTX v3 REST API.
type Client struct {
	Base string
	HTTP *http.Client
}

// New creates a Client for e.g. http://mediamtx:9997.
func New(base string) *Client {
	return &Client{
		Base: base,
		HTTP: &http.Client{Timeout: 5 * time.Second},
	}
}

// PathInfo is the subset of /v3/paths/get/{name} we care about.
type PathInfo struct {
	Ready   bool `json:"ready"`
	Readers []struct {
		Type string `json:"type"`
	} `json:"readers"`
	Source struct {
		Type string `json:"type"`
	} `json:"source"`
}

// Viewers returns the number of active readers.
func (p *PathInfo) Viewers() int {
	if p == nil {
		return 0
	}
	return len(p.Readers)
}

// GetPath fetches path info. Returns (nil, nil) when the path does not exist
// (stream offline / never published).
func (c *Client) GetPath(ctx context.Context, name string) (*PathInfo, error) {
	u := c.Base + "/v3/paths/get/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("mediamtx api %s: %s", resp.Status, body)
	}
	var info PathInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ---- runtime path configuration (recording control) ----

// HasPathConfig reports whether an explicit (config-level) path entry exists.
func (c *Client) HasPathConfig(ctx context.Context, name string) (bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v3/config/paths/get/"+url.PathEscape(name), nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, c.apiError(resp)
	}
}

// EnsurePathRecord creates a config-level path entry with recording enabled.
// It is a no-op when the entry already exists: patching an actively publishing
// path can interrupt the stream, so the entry is only created upfront.
// Runtime config does not survive MediaMTX restarts, so callers re-assert
// periodically. recordDeleteAfter keeps local disk clean (e.g. "168h").
func (c *Client) EnsurePathRecord(ctx context.Context, name, recordDeleteAfter string) error {
	exists, err := c.HasPathConfig(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	body := map[string]any{"record": true, "recordDeleteAfter": recordDeleteAfter}
	resp, err := c.doJSON(ctx, http.MethodPost, "/v3/config/paths/add/"+url.PathEscape(name), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	// Another writer may have added it concurrently: treat 409 as success.
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	return c.apiError(resp)
}

// DeletePathConfig removes the config-level path entry (best-effort cleanup).
func (c *Client) DeletePathConfig(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/v3/config/paths/delete/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return c.apiError(resp)
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, err
	}
	return c.HTTP.Do(req)
}

func (c *Client) doJSON(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.HTTP.Do(req)
}

func (c *Client) apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("mediamtx api %s: %s", resp.Status, body)
}
