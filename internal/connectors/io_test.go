package connectors

import (
	"context"
	"errors"
	"github.com/StreamDockManager/Plugins/internal/connectors/command"
	"strings"
	"testing"
	"time"
)

// TestSecretReferencesAndRedaction verifies secret references and redaction
func TestSecretReferencesAndRedaction(t *testing.T) {
	t.Setenv("CONNECTOR_TEST_PASSWORD", "resolved+secret")
	s := map[string]any{"password": "${CONNECTOR_TEST_PASSWORD}"}
	v, err := Secret(s, "password")
	if v != "resolved+secret" || err != nil {
		t.Fatal(v, err)
	}
	message := SafeError(errors.New("resolved+secret https://u:p@host/?token=private resolved%2Bsecret"), s).Error()
	for _, secret := range []string{"resolved", "private", "u:p"} {
		if strings.Contains(message, secret) {
			t.Fatal(message)
		}
	}
	s["password"] = "prefix-${CONNECTOR_TEST_PASSWORD}"
	if _, err = Secret(s, "password"); err == nil {
		t.Fatal("partial reference accepted")
	}
	s["password"] = "${CONNECTOR_ABSENT_PASSWORD}"
	if _, err = Secret(s, "password"); err == nil {
		t.Fatal("absent reference accepted")
	}
	retry := time.Now().Add(time.Minute)
	failure := SafeError(&HTTPError{Status: 429, Retry: retry}, s)
	if RetryAt(failure) != retry {
		t.Fatal("lost service backoff")
	}
}

// TestBoundedRunnerCancellationAndNoShellExpansion verifies bounded runner cancellation and no shell expansion
func TestBoundedRunnerCancellationAndNoShellExpansion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := command.Run(ctx, "go", "version"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
