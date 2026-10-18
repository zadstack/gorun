package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var errMetricoolProvider = errors.New("metricool: response could not be verified")

type metricoolClient struct{ transport http.RoundTripper }

func (c metricoolClient) request(ctx context.Context, method, path string, cfg metricoolConfig, token string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(raw)
	}
	q := url.Values{"userId": {cfg.UserID}, "blogId": {cfg.BlogID}}
	req, err := http.NewRequestWithContext(ctx, method, "https://app.metricool.com/api"+path+"?"+q.Encode(), body)
	if err != nil {
		return nil, 0, errMetricoolProvider
	}
	req.Header.Set("X-Mc-Auth", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 12 * time.Second, Transport: c.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, errMetricoolProvider
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, resp.StatusCode, errMetricoolProvider
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, errMetricoolProvider
	}
	return raw, resp.StatusCode, nil
}
func (c metricoolClient) test(ctx context.Context, cfg metricoolConfig, token string) error {
	raw, _, err := c.request(ctx, "GET", "/admin/simpleProfiles", cfg, token, nil)
	if err != nil {
		return err
	}
	var rows []struct {
		ID      int64 `json:"id"`
		Deleted bool  `json:"deleted"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		return errMetricoolProvider
	}
	for _, row := range rows {
		if strconv.FormatInt(row.ID, 10) == cfg.BlogID && !row.Deleted {
			return nil
		}
	}
	return errMetricoolProvider
}
func (c metricoolClient) create(ctx context.Context, cfg metricoolConfig, token string, in metricoolInput) (string, error) {
	payload := struct {
		Text            string              `json:"text"`
		Providers       []map[string]string `json:"providers"`
		PublicationDate map[string]string   `json:"publicationDate"`
		Draft           bool                `json:"draft"`
		AutoPublish     bool                `json:"autoPublish"`
	}{in.Text, []map[string]string{{"network": in.Network}}, map[string]string{"dateTime": in.DateTime, "timezone": in.Timezone}, true, false}
	raw, _, err := c.request(ctx, "POST", "/v2/scheduler/posts", cfg, token, payload)
	if err != nil {
		return "", err
	}
	var reply struct {
		Data struct {
			ID          int64 `json:"id"`
			Draft       bool  `json:"draft"`
			AutoPublish bool  `json:"autoPublish"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.Data.ID <= 0 || !reply.Data.Draft || reply.Data.AutoPublish {
		return "", errMetricoolProvider
	}
	return strconv.FormatInt(reply.Data.ID, 10), nil
}
