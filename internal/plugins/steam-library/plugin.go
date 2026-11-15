package steamlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	discovery "github.com/StreamDockManager/Plugin-SDK/Go/discovery"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/StreamDockManager/Plugin-SDK/Go"
	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/engine"
)

type Plugin struct {
	discovery discovery.Runner
	*engine.Engine
	State map[string]*saved
	index *steamIndex
}

type SteamGame struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Library         string `json:"library"`
	PlaytimeMinutes *int   `json:"playtime_minutes"`
}

type VDF map[string]any

type backgroundJob = engine.BackgroundJob

type binding = engine.Binding

type boundedOutput struct{ bytes.Buffer }

type displayStatus = engine.DisplayStatus

type jobResult = engine.JobResult

type saved struct {
	Value    float64  `json:"value"`
	Deadline int64    `json:"deadline"`
	Draws    []string `json:"draws"`
}

var action = engine.Action

var active = engine.Active

var cloneFeedback = engine.CloneFeedback

var emptySaved saved

// Discover installed Steam games for the configured root and account
func DiscoverSteam(ctx context.Context, root, account string) ([]SteamGame, error) {
	return discoverSteam(ctx, root, account, readVDF)
}

// Discover Steam games through an injectable VDF reader
func discoverSteam(ctx context.Context, root, account string, read func(string) (VDF, error)) ([]SteamGame, error) {
	scan, err := scanSteam(ctx, root, account, func(path string) (steamFileData, error) {
		data, err := read(path)
		if err != nil {
			return steamFileData{}, err
		}
		compact := compactSteamMetadata(path, data)
		compact.installed = steamInstalled(path, compact.app)
		return compact, nil
	})
	if err != nil {
		return nil, err
	}
	games, _ := scan.inventory()
	return games, nil
}

// Attach steam library lifecycle and action hooks to the shared engine
func Factory(base *engine.Engine) engine.Feature { return makePlugin(base).hooks() }

// Parse bounded Valve key-value metadata into nested objects
func ParseVDF(data string) (VDF, error) {
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("Steam metadata exceeds 8 MiB")
	}
	i := 0
	count := 0
	skip := func() {
		for i < len(data) {
			if unicode.IsSpace(rune(data[i])) {
				i++
				continue
			}
			if strings.HasPrefix(data[i:], "//") {
				for i < len(data) && data[i] != '\n' {
					i++
				}
				continue
			}
			break
		}
	}
	token := func() (string, error) {
		skip()
		if i >= len(data) {
			return "", fmt.Errorf("unexpected end of Steam metadata")
		}
		if data[i] == '{' || data[i] == '}' {
			v := string(data[i])
			i++
			return v, nil
		}
		var b strings.Builder
		if data[i] == '"' {
			i++
			for i < len(data) {
				c := data[i]
				i++
				if c == '"' {
					return b.String(), nil
				}
				if c == '\\' && i < len(data) {
					if data[i] == '\\' || data[i] == '"' {
						c = data[i]
						i++
					}
				}
				b.WriteByte(c)
			}
			return "", fmt.Errorf("unterminated Steam string")
		}
		for i < len(data) && !unicode.IsSpace(rune(data[i])) && data[i] != '{' && data[i] != '}' {
			b.WriteByte(data[i])
			i++
		}
		return b.String(), nil
	}
	var object func(int, bool) (VDF, error)
	object = func(depth int, nested bool) (VDF, error) {
		if depth > 32 {
			return nil, fmt.Errorf("Steam metadata nesting exceeds limit")
		}
		result := VDF{}
		for {
			skip()
			if i == len(data) {
				if nested {
					return nil, fmt.Errorf("unclosed Steam object")
				}
				return result, nil
			}
			key, err := token()
			if err != nil {
				return nil, err
			}
			if key == "}" {
				if !nested {
					return nil, fmt.Errorf("unexpected closing brace")
				}
				return result, nil
			}
			if key == "{" {
				return nil, fmt.Errorf("missing Steam key")
			}
			value, err := token()
			if err != nil {
				return nil, err
			}
			count++
			if count > 100000 {
				return nil, fmt.Errorf("too many Steam fields")
			}
			switch value {
			case "{":
				child, err := object(depth+1, true)
				if err != nil {
					return nil, err
				}
				result[key] = child
			case "}":
				return nil, fmt.Errorf("missing Steam value")
			default:
				result[key] = value
			}
		}
	}
	return object(0, false)
}

// Start a helper process with the requested arguments
func launch(ctx context.Context, name string, args ...string) (string, error) {
	return runHelper(exec.CommandContext(ctx, name, args...), name)
}

// Open the selected Steam application through its launch URL
func launchSteam(ctx context.Context, id string) error {
	if _, err := strconv.ParseUint(id, 10, 32); err != nil || id == "0" {
		return fmt.Errorf("Steam application ID must be a positive number")
	}
	url := "steam://rungameid/" + id
	var err error
	switch runtime.GOOS {
	case "darwin":
		_, err = launch(ctx, "open", url)
	case "windows":
		_, err = launch(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", url)
	default:
		_, err = launch(ctx, "xdg-open", url)
	}
	return err
}

// Initialize steam library state around the shared engine
func makePlugin(base *engine.Engine) *Plugin {
	e := &Plugin{Engine: base, State: map[string]*saved{}, index: &steamIndex{}}
	engine.Restore(e.Engine, &e.State)
	if e.State == nil {
		e.State = map[string]*saved{}
	}
	return e
}

// Apply completed steam library work on the engine owner loop
func (e *Plugin) applyResult(ctx context.Context, c *sdk.Client, result jobResult, job backgroundJob) error {

	return e.FinishJob(ctx, c, result, job)
}

// Build steam library status from the current asynchronous result
func (e *Plugin) asyncStatus(id string) displayStatus {
	return engine.AsyncStatus(e.ResultsText[id])
}

// Build the current steam library status for a binding
func (e *Plugin) displayStatus(ctx context.Context, id string, b binding) displayStatus {

	return e.extensionStatus(id, b)

}

// Handle steam library lifecycle events outside the common action path
func (e *Plugin) extensionEvent(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) (bool, error) {

	id := ev.Context
	a := action(ev.Action)
	s := e.MergedSettings(b.Settings)
	if ev.Event == "desktopInputResult" {
		var p map[string]any
		_ = json.Unmarshal(ev.Payload, &p)
		if message, ok := p["outcome"].(string); ok {
			e.ResultsText[id] = message
		}
		return true, e.Render(ctx, c, id)
	}

	if !active(ev) && ev.Event != "dialRotate" {
		return true, nil
	}
	{

		switch a {
		case "next":
			e.state("library", nil).Value++
		case "previous":
			e.state("library", nil).Value--
		case "favorite":
			appID := configvalue.String(s, "app_id", "")
			if _, err := strconv.ParseUint(appID, 10, 32); err != nil {
				return true, fmt.Errorf("choose a numeric game ID")
			}
			state := e.state("library", nil)
			exists := false
			for i, value := range state.Draws {
				if value == appID {
					copy(state.Draws[i:], state.Draws[i+1:])
					state.Draws[len(state.Draws)-1] = ""
					state.Draws = state.Draws[:len(state.Draws)-1]
					exists = true
					break
				}
			}
			if !exists {
				state.Draws = append(state.Draws, appID)
			}
		}
		e.startSteam(ctx, id, a, s)
		if err := engine.Persist(e.Engine); err != nil {
			return true, err
		}
	}

	return true, e.Render(ctx, c, id)
}

// Route steam library inspector requests through the supported handlers
func (e *Plugin) extensionInspector(ctx context.Context, c *sdk.Client, ev sdk.Event) (bool, error) {
	var p struct {
		Op        string         `json:"op"`
		RequestID string         `json:"request_id"`
		Settings  map[string]any `json:"settings"`
		Scope     string         `json:"scope"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if p.Op == "cancel_discovery" {
		e.discovery.Cancel(ev.Context, "steam", p.RequestID)
		return true, nil
	}
	if p.Op != "discover" && p.Op != "test" {
		return false, nil
	}
	s := e.MergedSettings(p.Settings)
	if p.Op == "discover" {
		return true, e.discovery.Run(ctx, e.Engine, c, discovery.Request{Context: ev.Context, Action: ev.Action, Channel: "steam", ID: p.RequestID, Operation: "steam_games", Scope: p.Scope}, 20*time.Second, func(job context.Context) (map[string]any, error) {
			games, err := e.index.discover(job, configvalue.String(s, "steam_path", ""), configvalue.String(s, "account", ""))
			devices := make([]map[string]string, 0, min(len(games), 512))
			for _, game := range games[:min(len(games), 512)] {
				devices = append(devices, map[string]string{"id": game.ID, "name": game.Name, "kind": "app_id"})
			}
			payload := map[string]any{"devices": devices}
			if err != nil {
				payload["message"] = "Could not read the Steam library"
			}
			return payload, err
		})
	}

	e.startSteam(ctx, ev.Context, "refresh", s)
	return true, nil
}

// Process due steam library extension work on the owner loop
func (e *Plugin) extensionPoll(ctx context.Context, c *sdk.Client) (bool, error) {

	for id := range e.Bindings {

		if err := e.Render(ctx, c, id); err != nil {
			return true, err
		}
	}
	return true, nil
}

// Select the steam library display state for extension-owned work
func (e *Plugin) extensionStatus(id string, _ binding) displayStatus {
	status := e.asyncStatus(id)

	if _, ok := e.Jobs[id]; ok {
		status.Icon = "icons/busy"
	}
	return status
}

// Dispatch an activating steam library input to its action implementation
func (e *Plugin) handleAction(ctx context.Context, c *sdk.Client, ev sdk.Event, b binding) error {
	_, err := e.extensionEvent(ctx, c, ev, b)
	return err
}

// Expose the steam library feature callbacks used by the shared engine
func (e *Plugin) hooks() engine.Feature {
	return engine.Feature{Clear: e.discovery.Clear, Close: e.discovery.Close, Action: e.handleAction, Inspector: e.extensionInspector, Status: e.displayStatus, Deadline: e.nextPoll, Snapshot: func() any { return e.State }, Apply: e.applyResult, Visuals: visuals, Poll: e.pollFeature}
}

// Find the saved steam library state selected by this binding
func (e *Plugin) lookupState(id string, settings map[string]any) *saved {
	return engine.LookupSaved(e.State, id, settings, &emptySaved)
}

// Choose the next explicit steam library deadline without idle polling
func (e *Plugin) nextPoll(now time.Time) time.Time {

	return e.PendingWrite

}

// Run due steam library feature work without blocking event dispatch
func (e *Plugin) pollFeature(ctx context.Context, c *sdk.Client) (bool, error) {
	return e.extensionPoll(ctx, c)
}

// Schedule bounded steam library work with binding ownership and result handling
func (e *Plugin) startJob(ctx context.Context, id string, work func(context.Context) (string, any, error)) bool {
	return e.StartJob(ctx, id, work, e.applyResult)
}

// Schedule Steam discovery or launch work for a binding
func (e *Plugin) startSteam(ctx context.Context, id, a string, s map[string]any) {
	index := int(e.lookupState("library", nil).Value)
	favorites := make(map[string]struct{})
	for _, id := range e.lookupState("library", nil).Draws {
		favorites[id] = struct{}{}
	}
	e.startJob(ctx, id, func(job context.Context) (string, any, error) {
		games, err := e.index.discover(job, configvalue.String(s, "steam_path", ""), configvalue.String(s, "account", ""))
		if err != nil {
			return "", nil, err
		}
		if configvalue.Bool(s, "favorites_only") {
			filtered := []SteamGame{}
			for _, game := range games {
				if _, ok := favorites[game.ID]; ok {
					filtered = append(filtered, game)
				}
			}
			if len(filtered) == 0 {
				return "", games, fmt.Errorf("no installed favorites selected")
			}
			games = filtered
		}
		selected := games[((index%len(games))+len(games))%len(games)]
		if appID := configvalue.String(s, "app_id", ""); appID != "" {
			found := false
			for _, game := range games {
				if game.ID == appID {
					selected = game
					found = true
					break
				}
			}
			if !found {
				return "", games, fmt.Errorf("selected game is not installed")
			}
		}
		if a == "launch" {
			if err := validateSteamInstallation(job, selected); err != nil {
				return "", games, fmt.Errorf("selected installation is no longer available: %w", err)
			}
			if err := launchSteam(job, selected.ID); err != nil {
				return "", games, err
			}
		}
		text := selected.Name
		if selected.PlaytimeMinutes != nil {
			text += fmt.Sprintf("\n%d min", *selected.PlaytimeMinutes)
		} else {
			text += "\nPlaytime unavailable"
		}
		return text, games, nil
	})
}

// Resolve or initialize the steam library state for a binding
func (e *Plugin) state(context string, s map[string]any) *saved {
	engine.MarkDirty(e.Engine)
	key := engine.SavedKey(context, s)
	v := engine.EnsureSaved(e.State, key)

	return v
}

// Read and parse a bounded Steam metadata file
func readVDF(path string) (VDF, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.Size() > 8<<20 {
		return nil, fmt.Errorf("Steam metadata exceeds limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	return ParseVDF(string(data))
}

// Run the prepared helper command with bounded output handling
func runHelper(cmd *exec.Cmd, name string) (string, error) {
	cmd.WaitDelay = 2 * time.Second
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s failed; check installation, selection and permissions", name)
	}
	return strings.TrimSpace(out.String()), nil
}

// Resolve the configured or platform-default Steam directory
func steamRoot(config string) string {
	if config != "" {
		return config
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library/Application Support/Steam")
	case "windows":
		return filepath.Join(os.Getenv("ProgramFiles(x86)"), "Steam")
	default:
		for _, p := range []string{filepath.Join(home, ".local/share/Steam"), filepath.Join(home, ".steam/steam")} {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return p
			}
		}
	}
	return filepath.Join(home, ".local/share/Steam")
}

// Interpret a parsed VDF value as an object
func vdfObject(value any) VDF {
	if m, ok := value.(VDF); ok {
		return m
	}
	return VDF{}
}

// Read a string field from parsed VDF metadata
func vdfString(m VDF, key string) string { s, _ := m[key].(string); return s }
