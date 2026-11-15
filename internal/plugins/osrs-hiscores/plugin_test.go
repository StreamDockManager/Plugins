package osrshiscores

import (
	"github.com/StreamDockManager/Plugins/internal/hiscores"
	"strings"
	"testing"
)

// Verify new skills and activities use provider i ds
func TestNewSkillsAndActivitiesUseProviderIDs(t *testing.T) {
	sample, err := hiscores.ParseJSON([]byte(`{"skills":[{"id":24,"name":"Sailing","rank":-1,"level":1,"xp":0},{"id":0,"name":"Overall","rank":2,"level":2376,"xp":4600000000}],"activities":[{"id":91,"name":"Zulrah","rank":12,"score":500}]}`))
	if err != nil {
		t.Fatal(err)
	}
	d := hiscores.New(Boards, nil)
	defer d.Close()
	if text := d.Display(sample, "skill", map[string]any{"skill": "24"}); !strings.Contains(text, "Sailing") {
		t.Fatal(text)
	}
	if text := d.Display(sample, "activity", map[string]any{"activity": "91"}); !strings.Contains(text, "Zulrah") {
		t.Fatal(text)
	}
	if len(Boards) != 4 || strings.Contains(Boards["ultimate"], "hardcore") {
		t.Fatal(Boards)
	}
}
