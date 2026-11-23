//go:build windows

package observation

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Query allows one native provider call in flight per plugin. A hung COM
// provider cannot strand additional threads when a caller times out.
type Query struct{ Slot chan struct{} }

func (q Query) Read(ctx context.Context, run func() (string, error)) (string, error) {
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	timeout := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("native status provider timed out")
	}
	select {
	case q.Slot <- struct{}{}:
	case <-probe.Done():
		return "", timeout()
	}
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-q.Slot }()
		text, err := run()
		done <- result{text, err}
	}()
	select {
	case v := <-done:
		return v.text, v.err
	case <-probe.Done():
		return "", timeout()
	}
}

// PowerShell's pipeline serializes a single row as an object and multiple rows as an array.
func Rows[T any](rows []T, empty string) (string, error) {
	if len(rows) == 0 {
		return empty, nil
	}
	var value any = rows
	if len(rows) == 1 {
		value = rows[0]
	}
	data, err := json.Marshal(value)
	if len(data) > 1<<20 {
		return "", errors.New("native status exceeds the output limit")
	}
	return string(data), err
}
