// Package gateway provides an HTTP client for communicating with homelab gateway services.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/lobo235/homelab-chatbot/internal/config"
)

// Client performs HTTP requests against gateway services.
type Client struct {
	http *http.Client
}

// NewClient creates a gateway client with sensible timeouts.
func NewClient() *Client {
	return &Client{
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// HealthResult holds the health status of a single gateway.
type HealthResult struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
}

// CheckHealth calls GET /health on the given gateway and returns the result.
func (c *Client) CheckHealth(ctx context.Context, gw config.GatewayConfig) HealthResult {
	result := HealthResult{Name: gw.Name, URL: gw.URL}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gw.URL+"/health", nil)
	if err != nil {
		result.Error = fmt.Sprintf("bad url: %v", err)
		return result
	}
	if gw.Key != "" {
		req.Header.Set("Authorization", "Bearer "+gw.Key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Healthy = true
	} else {
		result.Error = fmt.Sprintf("status %d", resp.StatusCode)
	}
	return result
}

// StopNomadJob calls DELETE /jobs/{name} on the nomad gateway.
func (c *Client) StopNomadJob(ctx context.Context, gw config.GatewayConfig, jobName string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, gw.URL+"/jobs/"+jobName, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if gw.Key != "" {
		req.Header.Set("Authorization", "Bearer "+gw.Key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("nomad gateway returned status %d", resp.StatusCode)
	}
	return nil
}
