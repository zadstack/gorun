package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/media"
)

const maxConnectionResponse = 1 << 20

func connectionHTTPClient() *http.Client {
	c := httpx.NewSafeClientOpts(httpx.SafeClientOptions{Timeout: 20 * time.Second})
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}
func decodeConnectionJSON(reader io.Reader, out any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, maxConnectionResponse+1))
	if err != nil {
		return errors.New("response unreadable")
	}
	if len(raw) > maxConnectionResponse {
		return errors.New("response too large")
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return errors.New("invalid JSON response")
	}
	return nil
}
func connectionStatus(resp *http.Response) error {
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	return nil
}
func probeAIConnection(ctx context.Context, client *http.Client, c connectionRecord, s connectionSecrets) (connectionProbeResult, error) {
	var result connectionProbeResult
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Endpoint, "/")+"/models", nil)
	if err != nil {
		return result, errConnectionInvalid
	}
	req.Header.Set("Accept", "application/json")
	if c.Provider == "anthropic" {
		req.Header.Set("x-api-key", s.Token)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return result, errors.New("provider could not be reached safely")
	}
	defer resp.Body.Close()
	if err = connectionStatus(resp); err != nil {
		return result, err
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = decodeConnectionJSON(resp.Body, &body); err != nil {
		return result, err
	}
	for _, model := range body.Data {
		if model.ID == c.Model {
			result.Count = len(body.Data)
			return result, nil
		}
	}
	return result, errors.New("selected model was not listed")
}
func probeConnection(ctx context.Context, c connectionRecord, s connectionSecrets) (connectionProbeResult, error) {
	if err := validateConnection(c); err != nil {
		return connectionProbeResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	client := connectionHTTPClient()
	switch c.Kind {
	case "ai":
		return probeAIConnection(ctx, client, c, s)
	case "mcp":
		return probeMCPConnection(ctx, client, c, s)
	case "s3":
		storage, err := media.NewS3Storage(media.S3Config{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, AccessKey: s.AccessKey, SecretKey: s.SecretKey}, nil, false)
		if err != nil {
			return connectionProbeResult{}, errors.New("bucket configuration incomplete")
		}
		err = storage.ProbeBucket(ctx, client)
		return connectionProbeResult{}, err
	}
	return connectionProbeResult{}, errConnectionInvalid
}
