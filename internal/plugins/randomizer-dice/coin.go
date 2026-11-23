package randomizerdice

import (
	"context"
	"crypto/rand"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"math/big"
	"time"
)

// Name the persisted face without inventing an idle result
func coinLabel(face int) string {
	if face == 1 {
		return "Heads"
	}
	if face == 2 {
		return "Tails"
	}
	return "Heads or Tails"
}

// Keep H and T engraved on the same rigid d2 faces throughout the flip
func coinGlyph(face, frame, variant int) string {
	model := landingModels[2]
	model.coin = true
	return model.renderRotated(landingPose(frame, variant), landingRotation(model, face, frame, variant))
}

// Sample one fair face and replace only this binding's pending flip
func (e *Plugin) flipCoin(ctx context.Context, c *sdk.Client, id string) error {
	n, err := rand.Int(rand.Reader, big.NewInt(2))
	if err != nil {
		return err
	}
	face := int(n.Int64()) + 1
	e.state(id, nil).CoinFace = face
	if err = e.routinePersist(); err != nil {
		return err
	}
	e.coins[id] = dieRoll{start: e.Time(), sides: 2, result: face, variant: int(e.rollSequence % 4)}
	e.rollSequence++
	return e.Render(ctx, c, id)
}

// Render the coin and outlined result for image and feedback hosts
func (e *Plugin) coinStatus(id string) displayStatus {
	face := e.lookupState(id, nil).CoinFace
	label := coinLabel(face)
	frame, variant := rollFrameCount, 0
	if roll, ok := e.coins[id]; ok {
		face, frame, variant = roll.result, roll.frame, roll.variant
		label = "Flipping…"
	}
	if face != 1 && face != 2 {
		face = 1
	}
	return displayStatus{Text: label, Icon: e.dieSVG(`<g transform="translate(2.4 0) scale(.8)">` + coinGlyph(face, frame, variant) + `</g><g fill="#f5f7fa" data-sdm-fill="ink" stroke="none">` + outlinedText(func() string {
		if frame < rollFrameCount {
			return "Flip"
		}
		if label == "Heads or Tails" {
			return "Flip coin"
		}
		return label
	}(), 12, 25, 27, 5) + `</g>`), Content: &engine.Presentation{Kind: "value", Primary: label, Label: "Heads or Tails"}}
}

// Advance visible flips and retry a failed final reveal
func (e *Plugin) pollCoins(ctx context.Context, c *sdk.Client, now time.Time) error {
	for id, roll := range e.coins {
		frame := int(min(now.Sub(roll.start), rollDuration) / rollFrameInterval)
		if frame <= roll.frame {
			continue
		}
		if frame == rollFrameCount {
			delete(e.coins, id)
		} else {
			next := roll
			next.frame = frame
			e.coins[id] = next
		}
		if err := e.Render(ctx, c, id); err != nil {
			e.coins[id] = roll
			return err
		}
	}
	return nil
}
