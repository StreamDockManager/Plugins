package hiscores

import (
	"encoding/json"
	"fmt"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"math"
	"strconv"

	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
)

// Skill IDs are Jagex identifiers, never positions in a response or dropdown
type Skill struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Cap      int    `json:"cap"`
	Cape     string `json:"cape"`
	Icon     string `json:"icon"`
	CapeSize [2]int `json:"cape_size"`
	IconSize [2]int `json:"icon_size"`
}

type Game struct {
	Slug   string
	Skills []Skill
}

// LoadGame loads and validates the selected hiscores game definition
func LoadGame(slug string, data []byte) Game {
	g := Game{Slug: slug}
	if err := json.Unmarshal(data, &g.Skills); err != nil {
		panic(err)
	}
	seen := map[string]bool{}
	total := 0
	for _, skill := range g.Skills {
		id, err := strconv.Atoi(skill.ID)
		if err != nil || id < 0 || seen[skill.ID] || skill.Name == "" || skill.Cape == "" || skill.Icon == "" {
			panic(fmt.Sprintf("invalid %s skill registry", slug))
		}
		seen[skill.ID] = true
		if id != 0 {
			total += skill.Cap
		}
	}
	if len(g.Skills) < 2 || g.Skills[0].ID != "0" || g.Skills[0].Cap != total {
		panic("skill registry total does not match skill caps")
	}
	return g
}

// Skill looks up the skill definition by identity
func (g Game) Skill(id string) (Skill, bool) {
	for _, skill := range g.Skills {
		if skill.ID == id {
			return skill, true
		}
	}
	return Skill{}, false
}

// RuntimeAssets collects the game assets needed at runtime
func (g Game) RuntimeAssets() []string {
	var paths []string
	for _, skill := range g.Skills {
		paths = append(paths, skill.Cape+".png", skill.Icon+".png")
	}
	return paths
}

// ArtworkRect is the available image area in the shared Hiscores layouts
func ArtworkRect(variant string, wide bool) [4]int {
	if wide {
		return [4]int{4, 3, 68, 94}
	}
	if variant == "level" {
		return [4]int{24, 0, 152, 58}
	}
	return [4]int{50, 0, 100, 40}
}

// FitArtwork centers the full source image within a layout's image area. Legacy
// pixmaps use cover sizing, so their rectangle must match the artwork's aspect
func (g Game) FitArtwork(asset string, rect [4]int, width, height int) [4]int {
	var size [2]int
	for _, skill := range g.Skills {
		switch asset {
		case skill.Cape:
			size = skill.CapeSize
		case skill.Icon:
			size = skill.IconSize
		}
	}
	if size[0] <= 0 || size[1] <= 0 || width <= 0 || height <= 0 {
		return rect
	}
	scale := math.Min(float64(rect[2]*width)/float64(200*size[0]), float64(rect[3]*height)/float64(100*size[1]))
	w := max(1, int(math.Round(float64(size[0])*scale*200/float64(width))))
	h := max(1, int(math.Round(float64(size[1])*scale*100/float64(height))))
	return [4]int{rect[0] + (rect[2]-w)/2, rect[1] + (rect[3]-h)/2, w, h}
}

// ActionFields returns the public settings schema for the action
func (g Game) ActionFields(action string, boards ...string) []settings.Field {
	player := metadata.Text("username", "Player name", "")
	player.Required = true
	board := metadata.Select("board", "Hiscores board", "normal", boards...)
	board.OptionLabels = map[string]string{"normal": "Normal", "ironman": "Ironman", "hardcore": "Hardcore Ironman", "ultimate": "Ultimate Ironman"}
	fields := []settings.Field{player, board}
	if action == "activity" {
		return append(fields, metadata.Device("activity", "Activity ID"))
	}
	if action == "overall" || action == "refresh" {
		return fields
	}
	skill := metadata.Select("skill", "Skill", "0")
	skill.OptionLabels = map[string]string{}
	for _, item := range g.Skills {
		skill.Options = append(skill.Options, item.ID)
		skill.OptionLabels[item.ID] = item.Name
	}
	fields = append(fields, skill)
	if action == "skill" {
		metric := metadata.Select("metric", "Value", "level", "level", "xp", "rank", "all")
		metric.OptionLabels = map[string]string{"level": "Level", "xp": "Experience", "rank": "Rank", "all": "Level, XP and rank"}
		fields = append(fields, metric)
	}
	return fields
}
