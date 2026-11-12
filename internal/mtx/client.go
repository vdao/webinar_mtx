// Package mtx is a minimal client for the MediaMTX REST API.
package mtx

import (
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
