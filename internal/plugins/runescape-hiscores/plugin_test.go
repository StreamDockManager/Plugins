package runescapehiscores

import (
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/hiscores"
	"strings"
	"testing"
)

// Verify maintained CSV mapping preserves activity ID gaps
func TestMaintainedCSVMappingPreservesActivityIDGaps(t *testing.T) {
	var csv strings.Builder
	for i := range Mapping.Skills {
		fmt.Fprintf(&csv, "1,%d,%d\n", i+1, int64(i)*200000000)
	}
	for i := range Mapping.Activities {
		fmt.Fprintf(&csv, "-1,%d\n", 100+i)
	}
	sample, err := hiscores.ParseCSV([]byte(csv.String()), Mapping)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := sample.Activities["15"]; exists {
		t.Fatal("removed activity ID invented")
	}
	if entry := sample.Activities["26"]; entry.Name != "RuneScore" || *entry.Score != 124 {
		t.Fatal("RuneScore remapped by CSV position", entry)
	}
	if entry := sample.Skills["29"]; entry.Name != "Necromancy" {
		t.Fatal(entry)
	}
	if _, err = hiscores.ParseCSV([]byte(csv.String()+"-1,0\n"), Mapping); err == nil {
		t.Fatal("changed mapping accepted")
	}
}
