package randomizerdice

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

// VisualFor returns an independent copy of the same action artwork used at runtime
func VisualFor(action string) engine.Visual { return engine.CloneVisual(visuals[action]) }

// Select unmarked baked animation frames for very large dice
const wideResultSides = 10_000

// Keep the rolling die in place when the result appears.
func (e *Plugin) displayView(id string, status displayStatus) engine.Display {
	view := engine.CoordinatedView(e.Engine, id, status)
	b := e.Bindings[id]
	if action(b.Action) == "heads-or-tails" && status.Icon != "" {
		// The outlined image owns its label on keypad and feedback surfaces
		view.Title = ""
		view.Image = status.Icon
		view.Feedback["icon"].(map[string]any)["value"] = status.Icon
		view.Feedback["icon"].(map[string]any)["rect"] = [4]int{0, 0, 200, 100}
		view.Feedback["line0"].(map[string]any)["value"] = ""
		view.Feedback["line1"].(map[string]any)["value"] = ""
		return view
	}
	if action(b.Action) == "magic-8-ball" && status.Icon != "" {
		if surface := b.Surface; surface != nil && surface.Width > surface.Height*13/10 && surface.Height > 0 && surface.Width <= 16384 && surface.Height <= 16384 {
			// The aspect-aware image owns all text and geometry on wide touch tiles.
			view.Image = status.Icon
			view.Feedback["icon"].(map[string]any)["value"] = status.Icon
			view.Feedback["icon"].(map[string]any)["rect"] = [4]int{0, 0, 200, 100}
			view.Feedback["line0"].(map[string]any)["value"] = ""
			view.Feedback["line1"].(map[string]any)["value"] = ""
			return view
		}
		view.Image = status.Icon
		view.Feedback["icon"].(map[string]any)["value"] = status.Icon
		view.Feedback["icon"].(map[string]any)["rect"] = [4]int{15, 0, 170, 85}
		view.Feedback["line0"].(map[string]any)["value"] = ""
		view.Feedback["line1"].(map[string]any)["rect"] = [4]int{4, 84, 192, 16}
		if _, rolling := e.balls[id]; !rolling && e.lookupState(id, nil).BallAnswer != "" {
			view.Feedback["icon"].(map[string]any)["rect"] = [4]int{0, 0, 200, 100}
			view.Feedback["line1"].(map[string]any)["value"] = ""
		}
		return view
	}
	if action(b.Action) != "dice" || status.Content == nil || status.Unavailable {
		return view
	}
	if status.Content.Kind != "command" && status.Content.Kind != "value" {
		return view
	}
	w, h := 112, 112
	if b.Surface != nil && b.Surface.Width > 0 && b.Surface.Height > 0 {
		w, h = b.Surface.Width, b.Surface.Height
	}
	if w > 16384 || h > 16384 {
		return view
	}
	size := min(w, h) * 86 / 100
	labelRect := [4]int{4, 79, 192, 20}
	x, y := (w-size)/2, 0
	if w > h*13/10 {
		size = min(w/2, h)
		x, y = (w/2-size)/2, (h-size)/2
		labelRect = [4]int{100, 28, 96, 44}
	}
	view.Feedback["icon"].(map[string]any)["rect"] = [4]int{x * 200 / w, y * 100 / h, size * 200 / w, size * 100 / h}
	label := view.Feedback["line1"].(map[string]any)
	label["rect"] = labelRect
	label["font"] = map[string]any{"size": 17, "fit": true}
	if strings.HasPrefix(status.Icon, "data:image/svg+xml;base64,") {
		view.Image = status.Icon
		view.Feedback["icon"].(map[string]any)["value"] = status.Icon
	}
	// The round badge owns the visible result, even on title-only hosts
	if status.Content.Kind == "value" {
		view.Title = status.Content.Label
	}
	view.Feedback["line0"].(map[string]any)["value"] = ""
	return view
}

// The pip pattern stays attached to its face throughout each possible d6 roll.
func dieFaceIcon(value int) string { return fmt.Sprintf("icons/die-face-%d", value) }

func cubeFrameIcon(value, frame int) string {
	if frame == rollFrameCount {
		return dieFaceIcon(value)
	}
	if value == 5 {
		return dieIcon(6, frame)
	}
	return fmt.Sprintf("icons/die-face-%d-%d", value, frame)
}

func dieIcon(sides int64, frame int) string {
	if sides >= wideResultSides && frame > rollLandingFrame {
		return fmt.Sprintf("icons/die-large-%d", frame)
	}
	return fmt.Sprintf("icons/die-%d-%d", modeledDieSides(sides), frame)
}

// Use a nearby bounded mesh while keeping the configured label and result exact
func modeledDieSides(sides int64) int64 {
	if sides <= 20 {
		return sides
	}
	best := highDieShapes[0]
	for _, shape := range highDieShapes[1:] {
		if sides <= (best+shape)/2 {
			break
		}
		best = shape
	}
	return best
}

func dieIconPaths() []string {
	var paths []string
	for _, sides := range modeledSides() {
		for frame := 0; frame <= rollFrameCount; frame++ {
			paths = append(paths, dieIcon(sides, frame))
		}
	}
	for value := 1; value <= 6; value++ {
		for frame := 0; frame <= rollFrameCount; frame++ {
			if value != 5 || frame == rollFrameCount {
				paths = append(paths, cubeFrameIcon(value, frame))
			}
		}
	}
	for frame := rollLandingFrame + 1; frame <= rollFrameCount; frame++ {
		paths = append(paths, dieIcon(wideResultSides, frame))
	}
	return paths
}

// Each sample starts on the same clock as the runtime. Two diminishing hops
// accompany a full turn, followed by a gentle rock back to upright.
type rollPose struct {
	angle, x, y, reveal float64
}

func poseAt(frame int) rollPose {
	t := float64(frame) / float64(rollFrameCount-1)
	const landing = float64(rollLandingFrame) / float64(rollFrameCount-1)
	spin := math.Sin(min(t/landing, 1) * math.Pi / 2)
	rock := (1 - math.Cos(max(0, (t-landing)/(1-landing))*math.Pi)) / 2
	return rollPose{
		angle:  -26 + 398*spin - 12*rock,
		x:      -2 * (1 - t),
		y:      -3 * (1 - t) * math.Abs(math.Sin(2*math.Pi*t)),
		reveal: rock,
	}
}

func modeledSides() []int64 {
	var sides []int64
	for n := int64(2); n <= 20; n++ {
		sides = append(sides, n)
	}
	return append(sides, highDieShapes[:]...)
}

// RollGlyphs generates themed artwork at package time. The running plugin
// only selects a packaged frame.
func RollGlyphs() map[string]string {
	glyphs := make(map[string]string)
	for _, sides := range modeledSides() {
		model := modelFor(sides)
		for frame := range rollFrameCount {
			pose := poseAt(frame)
			glyphs[dieIcon(sides, frame)] = model.render(pose)
			if sides == highDieShapes[len(highDieShapes)-1] && frame > rollLandingFrame {
				pose.reveal = 0 // Long results appear outside the die.
				glyphs[dieIcon(wideResultSides, frame)] = model.render(pose)
			}
		}
		glyphs[dieIcon(sides, rollFrameCount)] = glyphs[dieIcon(sides, rollFrameCount-1)]
	}
	glyphs[dieIcon(wideResultSides, rollFrameCount)] = glyphs[dieIcon(wideResultSides, rollFrameCount-1)]
	cube := modelFor(6)
	for i, marks := range cubeMarks {
		cube.marks = marks[:]
		for frame := range rollFrameCount {
			if i+1 != 5 {
				glyphs[cubeFrameIcon(i+1, frame)] = cube.render(poseAt(frame))
			}
		}
		glyphs[dieFaceIcon(i+1)] = glyphs[cubeFrameIcon(i+1, rollFrameCount-1)]
	}
	return glyphs
}

// Inline SVG bypasses packaged-asset theme rewriting; apply only validated
// negotiated color values, never inject arbitrary markup into a frame.
var landingColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func tintLandingSVG(svg string, colors sdk.ThemeColors) string {
	for _, pair := range [][2]string{{"#c61e32", colors.Accent}, {"#f5f7fa", colors.Ink}} {
		if landingColor.MatchString(pair[1]) {
			svg = strings.ReplaceAll(svg, `="`+pair[0]+`"`, `="`+pair[1]+`"`)
		}
	}
	return svg
}
