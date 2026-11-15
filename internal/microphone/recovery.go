package microphone

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// Only measured per-device, per-channel gains are persisted. Missing values never get defaults
func loadGains(path string) (map[string]float32, error) {
	values := map[string]float32{}
	bytes, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return values, nil
	}
	if e != nil {
		return nil, e
	}
	if len(bytes) > 1<<20 {
		return nil, fmt.Errorf("gain recovery state exceeds limit")
	}
	if e = json.Unmarshal(bytes, &values); e != nil {
		return nil, e
	}
	for _, v := range values {
		if v < 0 || v > 1 || math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("invalid saved input gain")
		}
	}
	return values, nil
}

// saveGains persists microphone gains needed for mute recovery
func saveGains(path string, values map[string]float32) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	bytes, e := json.Marshal(values)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".gain-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(bytes)
	}
	if e == nil {
		e = f.Sync()
	}
	closed := f.Close()
	if e != nil {
		return e
	}
	if closed != nil {
		return closed
	}
	return os.Rename(f.Name(), path)
}
