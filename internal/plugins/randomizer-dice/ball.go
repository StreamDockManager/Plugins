package randomizerdice

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugins/internal/engine"
	"math/big"
	"time"
)

var ballAnswers = []string{
	"Yes", "You bet", "Count on it", "Very likely", "Looks good", "Go for it", "Of course", "Signs say yes",
	"Ask again", "Wait and see", "Not yet clear", "Think it over", "Try later", "Maybe",
	"No", "Not likely", "Don't count on it", "Outlook poor", "Better not", "Signs say no",
}

type ballRoll struct {
	start  time.Time
	frame  int
	answer string
}

func (e *Plugin) shakeBall(ctx context.Context, c *sdk.Client, id string) error {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(ballAnswers))))
	if err != nil {
		return err
	}
	answer := ballAnswers[n.Int64()]
	// Dedicated field, no shared_id lookup: List/Draw/Dice and Reset stay independent.
	e.state(id, nil).BallAnswer = answer
	if err := e.routinePersist(); err != nil {
		return err
	}
	e.balls[id] = ballRoll{start: e.Time(), answer: answer}
	return e.Render(ctx, c, id)
}
func (e *Plugin) ballStatus(id string) displayStatus {
	answer := e.lookupState(id, nil).BallAnswer
	frame := rollFrameCount
	title := answer
	if roll, ok := e.balls[id]; ok {
		answer = roll.answer
		frame = roll.frame
		title = "Shaking…"
	}
	if title == "" {
		title = "Ask a question"
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><g transform="translate(4 4)">` + ballGlyph(frame, answer) + `</g></svg>`
	if answer != "" && frame >= rollFrameCount {
		svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">` + ballGlyph(frame, answer) + `</svg>`
	}
	if surface := e.Bindings[id].Surface; surface != nil && surface.Width > surface.Height*13/10 && surface.Height > 0 && surface.Width <= 16384 && surface.Height <= 16384 {
		svg = ballWideSVG(surface.Width, surface.Height, frame, answer)
	}
	return displayStatus{Text: title, Icon: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(tintBallSVG(svg, e.themeColors))), Content: &engine.Presentation{Kind: "value", Primary: title, Label: "Magic 8 Ball"}}
}
func (e *Plugin) pollBalls(ctx context.Context, c *sdk.Client, now time.Time) (bool, error) {
	for id, roll := range e.balls {
		frame := int(min(now.Sub(roll.start), rollDuration) / rollFrameInterval)
		if frame <= roll.frame {
			continue
		}
		if frame == rollFrameCount {
			delete(e.balls, id)
		} else {
			next := roll
			next.frame = frame
			e.balls[id] = next
		}
		if err := e.Render(ctx, c, id); err != nil {
			e.balls[id] = roll
			return true, err
		}
	}
	return true, nil
}
