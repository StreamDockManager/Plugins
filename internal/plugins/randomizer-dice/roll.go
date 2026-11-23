package randomizerdice

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type dieRoll struct {
	start   time.Time
	sides   int64
	frame   int
	result  int
	variant int
}

const (
	rollFrameInterval = 40 * time.Millisecond
	rollDuration      = 1200 * time.Millisecond
	rollFrameCount    = int(rollDuration / rollFrameInterval)
	rollLandingFrame  = 23
	badgeHoldFrames   = 6 // 240ms bare landed pose.
	badgeScaleFrames  = 6 // 240ms eased reveal.
	badgeFinalFrame   = rollFrameCount + badgeHoldFrames + badgeScaleFrames
)

func dieSides(settings map[string]any) (int64, error) {
	return integerSetting(settings, "sides", 6, 2, 1_000_000)
}

func (e *Plugin) dieStatus(id string, b binding) displayStatus {
	if roll, ok := e.rolls[id]; ok {
		label := fmt.Sprintf("d%d", roll.sides)
		badge := showResultBadge(b.Settings)
		var icon string
		if roll.sides <= 20 || (badge && roll.frame >= rollFrameCount) {
			icon = e.dieSVG(dieResultGlyph(roll.sides, roll.result, roll.frame, roll.variant, badge))
		} else {
			icon = dieIcon(roll.sides, min(roll.frame, rollFrameCount-1))
		}
		return displayStatus{Text: "Rolling " + label, Icon: icon,
			Content: &engine.Presentation{Kind: "command", Label: label}}
	}
	sides, err := dieSides(b.Settings)
	if err != nil {
		return engine.TextStatus("randomizer-dice", "dice", "", err)
	}
	label := fmt.Sprintf("d%d", sides)
	// A shared state name can be used by multiple dice or even a range.
	// Keep the result of this binding's own press until it is reconfigured.
	result, ok := e.Results[id].(float64)
	if !ok {
		result = e.lookupState(id, b.Settings).Value
	}
	if result < 1 || result > float64(sides) || math.Trunc(result) != result {
		return displayStatus{Text: label, Icon: dieIcon(sides, rollLandingFrame), Content: &engine.Presentation{Kind: "command", Label: label}}
	}
	glyph := dieResultGlyph(sides, int(result), badgeFinalFrame, 0, false)
	if fade, ok := e.badgeFades[id]; ok && showResultBadge(b.Settings) {
		glyph += resultBadgeGlyph(int(result), 1, fade.opacity())
	}
	icon := e.dieSVG(glyph)
	value := strconv.Itoa(int(result))
	return displayStatus{Text: value, Icon: icon, Content: &engine.Presentation{Kind: "value", Primary: value, Label: label}}
}

// Determine the first settled frame which can be followed by a total update
func (r dieRoll) finalFrame(settings map[string]any) int {
	if showResultBadge(settings) {
		return badgeFinalFrame
	}
	return rollFrameCount
}

// The bare landed pose is identical throughout the hold. Wake for the first
// visible badge step, including when other dice are still spinning.
func (r dieRoll) nextFrame() int {
	if r.frame >= rollFrameCount && r.frame <= rollFrameCount+badgeHoldFrames {
		return rollFrameCount + badgeHoldFrames + 1
	}
	return r.frame + 1
}

func (e *Plugin) nextRollFrame(_ time.Time) time.Time {
	var next time.Time
	for _, roll := range e.rolls {
		at := roll.start.Add(time.Duration(roll.nextFrame()) * rollFrameInterval)
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	// Keep the same reveal deadline if navigation or a settings change cancels a roll
	for id, held := range e.heldTotals {
		if _, rolling := e.rolls[id]; !rolling && (next.IsZero() || held.revealAt.Before(next)) {
			next = held.revealAt
		}
	}
	for _, roll := range e.balls {
		at := roll.start.Add(time.Duration(roll.frame+1) * rollFrameInterval)
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	for _, roll := range e.coins {
		at := roll.start.Add(time.Duration(roll.frame+1) * rollFrameInterval)
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	return e.nextBadgeFade(next)
}

func (e *Plugin) pollRolls(ctx context.Context, c *sdk.Client) (bool, error) {
	now := e.Time()
	for id, roll := range e.rolls {
		// Skip missed poses when a device is busy, keeping the reveal on time.
		finalFrame := roll.finalFrame(e.Bindings[id].Settings)
		frame := int(min(now.Sub(roll.start), time.Duration(finalFrame)*rollFrameInterval) / rollFrameInterval)
		if frame < min(finalFrame, roll.nextFrame()) {
			continue
		}
		previousBadge, hadBadge := e.badgeFades[id]
		if frame == finalFrame {
			delete(e.rolls, id)
			delete(e.badgeFades, id)
			if showResultBadge(e.Bindings[id].Settings) {
				e.badgeFades[id] = badgeFade{start: now.Add(badgeVisibleFor)}
			}
		} else {
			next := roll
			next.frame = frame
			e.rolls[id] = next
		}
		if err := e.Render(ctx, c, id); err != nil {
			e.rolls[id] = roll // Retry the display, including the final reveal
			if frame == finalFrame {
				delete(e.badgeFades, id)
				if hadBadge {
					e.badgeFades[id] = previousBadge
				}
			}
			return true, err
		}
	}
	// Publish the settled dice before exposing their values through the aggregate
	revealed := false
	for id, held := range e.heldTotals {
		if _, rolling := e.rolls[id]; !rolling && !now.Before(held.revealAt) {
			delete(e.heldTotals, id)
			revealed = true
		}
	}
	if revealed {
		if err := e.refreshCounters(c, totalCounterAction); err != nil {
			return true, err
		}
	}
	if err := e.pollBadgeFades(ctx, c, now); err != nil {
		return true, err
	}
	if err := e.pollCoins(ctx, c, now); err != nil {
		return true, err
	}
	return e.pollBalls(ctx, c, now)
}
