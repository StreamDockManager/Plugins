package randomizerdice

import (
	"context"
	"encoding/base64"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
)

const (
	badgeVisibleFor = 10 * time.Second
	badgeFadeSteps  = 10
)

type badgeFade struct {
	start time.Time
	step  int
}

// Calculate the remaining opacity of the result badge
func (f badgeFade) opacity() float64 { return 1 - float64(f.step)/badgeFadeSteps }

// Encode the complete die image for both image and feedback surfaces
func (e *Plugin) dieSVG(glyph string) string {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><g transform="translate(4 4)">` + glyph + `</g></svg>`
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(tintLandingSVG(svg, e.themeColors)))
}

// Only the medallion changes during a large die's reveal and fade.
var largeLandedGlyphs = func() map[int64]string {
	glyphs := make(map[int64]string, len(highDieShapes))
	for _, sides := range highDieShapes {
		glyphs[sides] = modelFor(sides).render(poseAt(rollFrameCount - 1))
	}
	return glyphs
}()

// Keep the numbered landing face or use a bounded unmarked mesh for larger dice
func dieResultGlyph(sides int64, result, frame, variant int, badge bool) string {
	if sides <= 20 {
		return configuredLandingGlyph(sides, result, frame, variant, badge)
	}
	glyph := largeLandedGlyphs[modeledDieSides(sides)]
	if badge && frame > rollFrameCount+badgeHoldFrames {
		t := min(1, float64(frame-rollFrameCount-badgeHoldFrames)/badgeScaleFrames)
		glyph += resultBadgeGlyph(result, t*t*(3-2*t), 1)
	}
	return glyph
}

// Sleep through the badge hold and schedule only the short fade animation
func (e *Plugin) nextBadgeFade(next time.Time) time.Time {
	for id, fade := range e.badgeFades {
		if _, rolling := e.rolls[id]; rolling {
			continue
		}
		at := fade.start.Add(time.Duration(fade.step+1) * rollFrameInterval)
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	return next
}

// Redraw just the badge alpha until the unobscured die is visible again
func (e *Plugin) pollBadgeFades(ctx context.Context, c *sdk.Client, now time.Time) error {
	for id, fade := range e.badgeFades {
		if _, rolling := e.rolls[id]; rolling {
			continue
		}
		step := int(min(now.Sub(fade.start), badgeFadeSteps*rollFrameInterval) / rollFrameInterval)
		if step <= fade.step {
			continue
		}
		if step == badgeFadeSteps {
			delete(e.badgeFades, id)
		} else {
			e.badgeFades[id] = badgeFade{start: fade.start, step: step}
		}
		if err := e.Render(ctx, c, id); err != nil {
			e.badgeFades[id] = fade
			return err
		}
	}
	return nil
}
