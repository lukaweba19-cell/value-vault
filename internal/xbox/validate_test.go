package xbox

import (
	"errors"
	"strings"
	"testing"
)

// Network errors from proxied requests can embed the full proxy URL. Those
// strings end up in the activity log, so credentials must never survive.
func TestSoftReasonMasksProxyCredentials(t *testing.T) {
	err := errors.New(`Get "https://login.live.com/": proxyconnect tcp: dial tcp http://user:secr3t@1.2.3.4:8080: i/o timeout`)
	got := softReason("preflight", err)

	if !strings.HasPrefix(got, "preflight failed: ") {
		t.Fatalf("stage prefix missing: %q", got)
	}
	if strings.Contains(got, "secr3t") {
		t.Fatalf("proxy password leaked into reason: %q", got)
	}
	if !strings.Contains(got, "http://***@1.2.3.4:8080") {
		t.Fatalf("expected masked proxy URL, got %q", got)
	}
}

func TestSoftReasonPlainErrorUnchanged(t *testing.T) {
	got := softReason("oauth", errors.New("context deadline exceeded"))
	if got != "oauth failed: context deadline exceeded" {
		t.Fatalf("unexpected: %q", got)
	}
}
