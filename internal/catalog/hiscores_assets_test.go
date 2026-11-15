package catalog

import (
	"bytes"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/fixtureengine"
	"image/png"
	"strings"
	"testing"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"github.com/StreamDockManager/Plugins/internal/hiscores"
	osrs "github.com/StreamDockManager/Plugins/internal/plugins/osrs-hiscores"
	rs "github.com/StreamDockManager/Plugins/internal/plugins/runescape-hiscores"
)

var sdkSurface = sdk.Surface{Width: 320, Height: 100}

// TestHiscoresRegistriesNamedSelectionsAndPackagedViews verifies hiscores registries named selections and packaged views
func TestHiscoresRegistriesNamedSelectionsAndPackagedViews(t *testing.T) {
	for _, tc := range []struct {
		game         hiscores.Game
		def          Definition
		new          func(string) *engine.Engine
		count, total int
	}{
		{osrs.Game, osrs.Definition, func(path string) *engine.Engine { return fixtureengine.New(osrs.Definition, path, osrs.Factory) }, 25, 2376}, {rs.Game, rs.Definition, func(path string) *engine.Engine { return fixtureengine.New(rs.Definition, path, rs.Factory) }, 30, 3232},
	} {
		t.Run(tc.def.Slug, func(t *testing.T) {
			if len(tc.game.Skills) != tc.count || tc.game.Skills[0].Cap != tc.total {
				t.Fatal(tc.game)
			}
			files := PluginAssets(tc.def)
			if err := ValidateAssets(tc.def, files); err != nil {
				t.Fatal(err)
			}
			for _, skill := range tc.game.Skills {
				for _, asset := range []string{skill.Cape, skill.Icon} {
					name := asset + ".png"
					original, err := hiscoresArtwork.ReadFile("artwork/" + name)
					if err != nil || !bytes.Equal(files[name], original) {
						t.Fatal("game artwork was altered or omitted", name, err)
					}
					image, err := png.Decode(bytes.NewReader(files[name]))
					if err != nil {
						t.Fatal("game artwork must be directly decodable PNG", name, err)
					}
					size := skill.CapeSize
					if asset == skill.Icon {
						size = skill.IconSize
					}
					if size != [2]int{image.Bounds().Dx(), image.Bounds().Dy()} {
						t.Fatal("artwork dimensions do not match the packaged image", name, size, image.Bounds())
					}
					visible := false
					for y := image.Bounds().Min.Y; y < image.Bounds().Max.Y && !visible; y++ {
						for x := image.Bounds().Min.X; x < image.Bounds().Max.X; x++ {
							if _, _, _, alpha := image.At(x, y).RGBA(); alpha > 0 {
								visible = true
								break
							}
						}
					}
					if !visible {
						t.Fatal("game artwork is blank", name)
					}
					delete(files, name)
					if ValidateAssets(tc.def, files) == nil {
						t.Fatal("missing game artwork was accepted", name)
					}
					files[name] = original
				}
			}
			fields := ActionFields(tc.def.Slug, "skill")
			value, err := settings.Validate(fields, map[string]any{"username": "Echolocate", "skill": "1", "metric": "all", "unknown": true})
			if err != nil || value["skill"] != "1" || value["metric"] != "all" || value["unknown"] != true {
				t.Fatal(value, err)
			}
			for _, field := range fields {
				if field.Key == "skill" && (field.Type != "select" || field.OptionLabels["1"] != "Attack" || len(field.Options) != tc.count) {
					t.Fatal(field)
				}
			}
			for _, action := range []string{"overall", "refresh"} {
				for _, field := range ActionFields(tc.def.Slug, action) {
					if field.Key == "skill" {
						t.Fatal("irrelevant skill control", action)
					}
				}
			}
			e := tc.new(t.TempDir() + "/state.json")
			defer e.Close()
			for _, theme := range []string{"light", "dark"} {
				for _, wide := range []bool{false, true} {
					e.Theme = theme
					b := engine.Binding{Action: ID(tc.def.Slug) + ".skill"}
					if wide {
						b.Surface = &sdkSurface
					}
					e.Bindings["fixture"] = b
					loading := e.DisplayView("fixture", engine.DisplayStatus{Text: "Loading", Icon: tc.game.Skills[1].Cape})
					if loading.Image != tc.game.Skills[1].Cape+".png" || loading.Feedback["icon"].(map[string]any)["value"] != loading.Image {
						t.Fatal("loading display lost its raster artwork", loading)
					}
					numberView := e.DisplayView("fixture", engine.DisplayStatus{Text: "Invention\n9007199254740993\nExperience", Icon: tc.game.Skills[1].Icon})
					if !strings.Contains(numberView.Layout, "number-") || numberView.Feedback["value"] != "9007199254740993" || numberView.Image != tc.game.Skills[1].Icon+".png" {
						t.Fatal("large XP value lost", numberView)
					}
					for _, skill := range tc.game.Skills {
						status := engine.DisplayStatus{Text: skill.Name + "\n80/" + fmt.Sprint(skill.Cap), Icon: skill.Cape}
						view := e.DisplayView("fixture", status)
						if len(files[view.Layout]) == 0 || len(files[view.Image]) == 0 || view.Feedback["value"] != "80/"+fmt.Sprint(skill.Cap) {
							t.Fatal(view)
						}
						if !bytes.Equal(files[view.Image], files[skill.Cape+".png"]) || view.Feedback["cape"].(map[string]any)["value"] != view.Image {
							t.Fatal("theme recolored game art", skill.ID)
						}
					}
				}
			}
		})
	}
	for _, tc := range []struct {
		id  string
		cap int
	}{{"1", 120}, {"15", 110}, {"4", 99}, {"29", 120}} {
		if s, _ := rs.Game.Skill(tc.id); s.Cap != tc.cap {
			t.Fatal(s)
		}
	}
	if s, _ := osrs.Game.Skill("24"); s.Name != "Sailing" || s.Cap != 99 {
		t.Fatal(s)
	}
}
