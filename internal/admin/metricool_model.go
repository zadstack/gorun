package admin

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

const metricoolConfigKey = "metricool.config"
const metricoolDraftPrefix = "metricool.draft."

var errMetricoolInvalid = errors.New("invalid Metricool input")
var errMetricoolConflict = errors.New("metricool: request changed")
var metricoolID = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

type metricoolConfig struct {
	Version    string `json:"version"`
	UserID     string `json:"user_id"`
	BlogID     string `json:"blog_id"`
	Secret     string `json:"secret"`
	TestedAt   string `json:"tested_at"`
	TestStatus string `json:"test_status"`
}
type metricoolInput struct{ Text, Network, DateTime, Timezone string }
type metricoolDraft struct {
	ID         string `json:"id"`
	Actor      string `json:"actor"`
	Hash       string `json:"hash"`
	Status     string `json:"status"`
	ProviderID string `json:"provider_id"`
	CreatedAt  string `json:"created_at"`
}

func validMetricoolInput(in metricoolInput, now time.Time) bool {
	if strings.TrimSpace(in.Text) == "" || len(in.Text) > 12000 || strings.ContainsRune(in.Text, 0) {
		return false
	}
	switch in.Network {
	case "facebook", "twitter", "linkedin", "threads", "bluesky":
	default:
		return false
	}
	if len(in.Timezone) > 80 {
		return false
	}
	loc, err := time.LoadLocation(in.Timezone)
	if err != nil {
		return false
	}
	when, err := time.ParseInLocation("2006-01-02T15:04:05", in.DateTime, loc)
	return err == nil && when.After(now) && when.Before(now.AddDate(2, 0, 0))
}
