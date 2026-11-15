package catalog

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"reflect"
	"testing"
)

// TestOwnedVisualsAreIndependent verifies owned visuals are independent
func TestOwnedVisualsAreIndependent(t *testing.T) {
	for _, def := range Definitions {
		for _, action := range def.Actions {
			first := VisualFor(def.Slug, action)
			second := actionVisuals[def.Slug](action)
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("%s/%s metadata mismatch", def.Slug, action)
			}
			if len(first.States) == 0 {
				t.Fatalf("%s/%s missing states", def.Slug, action)
			}
			first.States[0] = "mutated"
			if !reflect.DeepEqual(VisualFor(def.Slug, action), second) {
				t.Fatal("catalog mutated runtime visuals")
			}
			capability := Descriptor(def).Actions[ID(def.Slug)+"."+action]
			if capability.AdditiveDial != engine.AdditiveDial(def.Slug, action) {
				t.Fatal("additive metadata differs")
			}
		}
	}
}

// TestBasicFieldConstructorsStayIndependent verifies basic field constructors stay independent
func TestBasicFieldConstructorsStayIndependent(t *testing.T) {
	first, second := metadata.Numeric("value", "Value", 1, 0, 10), metadata.Numeric("value", "Value", 1, 0, 10)
	*first.Min = 7
	if *second.Min != 0 {
		t.Fatal("numeric bounds alias")
	}
	left, right := metadata.Select("mode", "Mode", "one", "one", "two"), metadata.Select("mode", "Mode", "one", "one", "two")
	left.Options[0] = "changed"
	if right.Options[0] != "one" {
		t.Fatal("select options alias")
	}
}
