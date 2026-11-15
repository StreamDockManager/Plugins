package randommeme

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	config "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type pendingInput struct {
	request   string
	operation string
	at        time.Time
}
type Plugin struct {
	*engine.Engine
	API     *API
	Current map[string]*Meme
	Pending map[string]pendingInput
}

// Attach random meme lifecycle and action hooks to the shared engine
func Factory(e *engine.Engine) engine.Feature {
	p := &Plugin{Engine: e, API: newAPI(), Current: map[string]*Meme{}, Pending: map[string]pendingInput{}}
	return p.hooks()
}

// Resolve the meme-sharing group from binding settings
func group(b engine.Binding) string { return config.String(b.Settings, "shared_id", "memes") }

// Read the group meme while honoring the current content filter
func (p *Plugin) current(b engine.Binding) *Meme {
	meme := p.Current[group(b)]
	if meme != nil && meme.NSFW && !config.Bool(p.Global, "allow_nsfw") {
		return nil
	}
	return meme
}

// Check whether a meme group already owns background work
func (p *Plugin) busy(key string) bool {
	for id := range p.Jobs {
		if group(p.Bindings[id]) == key {
			return true
		}
	}
	return false
}

// Build a meme action prompt from its current fetch or input state
func (p *Plugin) status(_ context.Context, id string, b engine.Binding) engine.DisplayStatus {
	text := p.ResultsText[id]
	if text == "" {
		text = "Fetch meme"
		if engine.Action(b.Action) == "send" || engine.Action(b.Action) == "copy" {
			text = "Fetch first"
		}
	}
	if p.current(b) != nil && text == "Fetch first" {
		text = linkLabel(engine.Action(b.Action))
	}
	return engine.DisplayStatus{Text: text}
}

// Dispatch meme fetch and link-input actions with correlated completion
func (p *Plugin) action(ctx context.Context, c *sdk.Client, ev sdk.Event, b engine.Binding) error {
	if ev.Event == "desktopInputResult" {
		var result struct {
			Request string `json:"request_id"`
			Outcome string `json:"outcome"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(ev.Payload, &result) != nil || p.Pending[ev.Context].request == "" || result.Request != p.Pending[ev.Context].request {
			return nil
		}
		pending := p.Pending[ev.Context]
		delete(p.Pending, ev.Context)
		text := "Pasted"
		if result.Error != "" {
			text = "Input failed"
		} else if pending.operation == "copy" || strings.Contains(strings.ToLower(result.Outcome), "copied") {
			text = "Copied to clipboard; paste unavailable"
			if pending.operation == "copy" {
				text = "Link copied"
			}
		}
		p.ResultsText[ev.Context] = text
		return p.Render(ctx, c, ev.Context)
	}
	if !engine.Active(ev) {
		return nil
	}
	key := group(b)
	if key == "" || len(key) > 128 {
		return fmt.Errorf("set a meme group of 1–128 characters")
	}
	switch engine.Action(b.Action) {
	case "fetch":
		if len(p.Jobs) > 0 {
			return fmt.Errorf("wait for the current Fetch to finish")
		}
		if _, exists := p.Current[key]; !exists && len(p.Current) >= 32 {
			return fmt.Errorf("maximum of 32 meme groups reached")
		}
		subreddit := strings.TrimSpace(config.String(b.Settings, "subreddit", ""))
		allowNSFW := config.Bool(p.Global, "allow_nsfw")
		p.StartJob(ctx, ev.Context, func(ctx context.Context) (string, any, error) {
			meme, err := p.API.Fetch(ctx, subreddit, allowNSFW)
			return "Ready", meme, err
		}, func(ctx context.Context, c *sdk.Client, result engine.JobResult, job engine.BackgroundJob) error {
			p.ResultsText[result.ID] = "Fetch failed"
			if result.Err == nil {
				meme, ok := result.Value.(*Meme)
				if !ok || meme == nil {
					return fmt.Errorf("meme response missing")
				}
				p.Current[key] = meme
				for id, binding := range p.Bindings {
					if group(binding) == key {
						p.ResultsText[id] = "Fetch again"
						if engine.Action(binding.Action) == "send" || engine.Action(binding.Action) == "copy" {
							p.ResultsText[id] = linkLabel(engine.Action(binding.Action))
						}
					}
				}
			}
			message := "Meme ready; Copy link or select an input box and press Paste link"
			if result.Err != nil {
				message = result.Err.Error()
			}
			if err := c.Send(ctx, sdk.Message("sendToPropertyInspector", result.ID, map[string]any{"message": message})); err != nil {
				return err
			}
			return p.Render(ctx, c, result.ID)
		})
	case "send", "copy":
		if p.busy(key) {
			return fmt.Errorf("wait for Fetch to finish before sending")
		}
		if _, busy := p.Pending[ev.Context]; busy {
			return nil
		}
		meme := p.current(b)
		if meme == nil {
			return fmt.Errorf("Fetch a meme in the same group first")
		}
		if err := imageURL(meme.URL); err != nil {
			return err
		}
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}
		request := hex.EncodeToString(token[:])
		operation := "copy"
		p.ResultsText[ev.Context] = "Copying…"
		if engine.Action(b.Action) == "send" {
			operation = "paste"
			// Capture only for paste. Neither action submits a message
			if err := c.DesktopInput(ctx, ev.Context, map[string]any{"op": "captureTarget", "request_id": request + "-target"}); err != nil {
				return err
			}
			p.ResultsText[ev.Context] = "Pasting…"
		}
		p.Pending[ev.Context] = pendingInput{request: request, operation: operation, at: time.Now()}
		if err := c.DesktopInput(ctx, ev.Context, map[string]any{"op": operation, "request_id": request, "text": meme.URL}); err != nil {
			delete(p.Pending, ev.Context)
			return err
		}
	}
	return p.Render(ctx, c, ev.Context)
}

// Expose the random meme feature callbacks used by the shared engine
func (p *Plugin) hooks() engine.Feature {
	return engine.Feature{Action: p.action, Status: p.status, Visuals: visuals, DynamicDisplay: true,
		View: func(id string, status engine.DisplayStatus) engine.Display {
			return View(p.Bindings[id], p.current(p.Bindings[id]), status.Text, p.Theme)
		},
		After: func(ctx context.Context, c *sdk.Client, ev sdk.Event) error {
			if ev.Event == "willAppear" || ev.Event == "didReceiveSettings" {
				b := p.Bindings[ev.Context]
				// The host may create a binding without opening its inspector
				// Resolve the declared group default before shared-view routing
				b.Settings = engine.CloneSettings(b.Settings)
				b.Settings["shared_id"] = group(b)
				p.Bindings[ev.Context] = b
			}
			if ev.Event != "didReceiveGlobalSettings" {
				return nil
			}
			p.clearFiltered()
			for id := range p.Bindings {
				if err := p.Render(ctx, c, id); err != nil {
					return err
				}
			}
			return nil
		},
		Inspector: func(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
			return true, c.Send(ctx, sdk.Message("sendToPropertyInspector", ev.Context, map[string]any{"message": "Use Fetch meme to preview, then Copy link or Paste link into the selected input. Inspector tests do not access the clipboard."}))
		},
		Clear: func(id string) { delete(p.Pending, id) },
		Deadline: func(time.Time) time.Time {
			var next time.Time
			for _, v := range p.Pending {
				at := v.at.Add(10 * time.Second)
				if next.IsZero() || at.Before(next) {
					next = at
				}
			}
			return next
		},
		Poll: func(ctx context.Context, c *sdk.Client) (bool, error) {
			for id, v := range p.Pending {
				if time.Since(v.at) >= 10*time.Second {
					delete(p.Pending, id)
					p.ResultsText[id] = "Input timed out"
					if err := p.Render(ctx, c, id); err != nil {
						return true, err
					}
				}
			}
			return false, nil
		},
	}
}

// Remove retained memes that are disallowed by current settings
func (p *Plugin) clearFiltered() {
	if config.Bool(p.Global, "allow_nsfw") {
		return
	}
	for key, meme := range p.Current {
		if meme.NSFW {
			delete(p.Current, key)
			for id, b := range p.Bindings {
				if group(b) == key {
					delete(p.ResultsText, id)
				}
			}
		}
	}
}

// Choose the prompt for copying or pasting a meme link
func linkLabel(action string) string {
	if action == "copy" {
		return "Copy link"
	}
	return "Paste link"
}
