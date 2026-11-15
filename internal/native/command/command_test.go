package command

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const backgroundHelperEnv = "STREAM_DOCK_MANAGER_TEST_BACKGROUND_COMMAND"

// TestBackgroundCommandPreservesIOAndExitStatus verifies background command preserves io and exit status
func TestBackgroundCommandPreservesIOAndExitStatus(t *testing.T) {
	cmd := BackgroundCommand(os.Args[0], "-test.run=^TestBackgroundCommandHelperProcess$")
	cmd.Env = append(os.Environ(), backgroundHelperEnv+"=io")
	cmd.Stdin = strings.NewReader("background helper input")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("expected exit 7, got %v; stderr=%q", err, stderr.String())
	}
	if string(stdout) != "0:background helper input" {
		t.Fatalf("console must be absent and stdin preserved: stdout=%q", stdout)
	}
	if stderr.String() != "expected failure" {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

// TestBackgroundCommandContextCancelsStartedProcess verifies background command context cancels started process
func TestBackgroundCommandContextCancelsStartedProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := BackgroundCommandContext(ctx, os.Args[0], "-test.run=^TestBackgroundCommandHelperProcess$")
	cmd.Env = append(os.Environ(), backgroundHelperEnv+"=wait")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	t.Cleanup(func() {
		cancel()
		if !waited {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("cancelled background helper did not exit")
			}
		}
	})
	select {
	case line := <-ready:
		if line != "0:ready\n" {
			t.Fatalf("console must be absent before cancellation: stdout=%q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background helper did not become ready")
	}
	cancel()
	select {
	case err = <-done:
		waited = true
		if err == nil || cmd.ProcessState.Success() {
			t.Fatalf("context cancellation did not terminate helper: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background helper ignored context cancellation")
	}
}

// The test executable is the child, so this exercises native process creation
// without PowerShell, installed third-party tools, GUI actions, or audio capture
func TestBackgroundCommandHelperProcess(t *testing.T) {
	switch os.Getenv(backgroundHelperEnv) {
	case "io":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(8)
		}
		fmt.Fprintf(os.Stdout, "%d:%s", testConsoleWindow(), input)
		fmt.Fprint(os.Stderr, "expected failure")
		os.Exit(7)
	case "wait":
		fmt.Fprintf(os.Stdout, "%d:ready\n", testConsoleWindow())
		for {
			time.Sleep(time.Hour)
		}
	}
}
