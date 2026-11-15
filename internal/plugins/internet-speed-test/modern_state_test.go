package internetspeedtest

import (
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"
)

// Historical state is deliberately larger; meaningful JSON fields must survive
type legacyModernState struct {
	SpeedResults []SpeedResult `json:"speed_results,omitempty"`
	Arrangement  string        `json:"arrangement"`
	FocusMillis  int64         `json:"focus_ms"`
	BreakMillis  int64         `json:"break_ms"`
	Value        float64       `json:"value"`
	Deadline     int64         `json:"deadline"`
	Started      int64         `json:"started"`
	Elapsed      int64         `json:"elapsed"`
	Mode         string        `json:"mode"`
	Break        bool          `json:"break"`
	Draws        []string      `json:"draws"`
	Palette      []string      `json:"palette"`
	Color        string        `json:"color"`
}

// Verify modern state keeps meaningful legacy values
func TestModernStateKeepsMeaningfulLegacyValues(t *testing.T) {
	var previous legacyModernState
	value := reflect.ValueOf(&previous).Elem()
	for _, field := range value.Fields() {
		switch field.Kind() {
		case reflect.String:
			field.SetString("retained")
		case reflect.Int64:
			field.SetInt(42)
		case reflect.Float64:
			field.SetFloat(42.5)
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.String {
				field.Set(reflect.ValueOf([]string{"one", "two"}))
			} else {
				field.Set(reflect.MakeSlice(field.Type(), 1, 1))
			}
		}
	}
	data, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	var current saved
	if err = json.Unmarshal(data, &current); err != nil {
		t.Fatal(err)
	}
	next, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal(data, &before)
	_ = json.Unmarshal(next, &after)
	for key, value := range after {
		if string(value) != string(before[key]) {
			t.Fatalf("meaningful field %s changed: %s -> %s", key, before[key], value)
		}
	}
	if unsafe.Sizeof(current) >= unsafe.Sizeof(previous) {
		t.Fatal("state allocation did not shrink")
	}
	t.Logf("state bytes: %d -> %d", unsafe.Sizeof(previous), unsafe.Sizeof(current))
}
