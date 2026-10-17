package admin

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
)

func TestCoreConnectionProbeCancelledBeforeAnyDNS(t *testing.T) {
	original := net.DefaultResolver
	var calls atomic.Int64
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("fixture denies every DNS call")
	}}
	t.Cleanup(func() { net.DefaultResolver = original })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, kind := range []string{"ai", "mcp", "s3"} {
		c := connectionRecord{Name: "Cancelled fixture", Kind: kind, Provider: "openai", Endpoint: "https://provider.example.invalid", Model: "fixture-model", Bucket: "fixturebucket", Region: "auto"}
		result, err := probeConnection(ctx, c, connectionSecrets{Token: "synthetic-token", AccessKey: "synthetic-access", SecretKey: "synthetic-secret"})
		if err == nil || result.Count != 0 || result.Protocol != "" {
			t.Fatal("cancelled probe claimed reachability", kind)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("cancelled probe tried DNS", calls.Load())
	}
}
