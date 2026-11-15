package makeplugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	operations "github.com/StreamDockManager/Plugin-SDK/Go/operations"
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var Icons = map[string]string{"run": "play", "activate": "check", "deactivate": "stop", "status": "network", "execution": "latency", "webhook": "upload"}
var Definition, visuals = c.Define("make", "Make", Icons, "run", "activate", "deactivate", "status", "execution", "webhook")

// Declare the make global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Select("region", "Make API region", "eu1", "eu1", "eu2", "us1", "us2"), metadata.Text("team_id", "Make team ID", ""), metadata.Sensitive("token", "Make API token")}
}

// Declare settings supported by the selected make action
func ActionFields(a string) []settings.Field {
	if a == "webhook" {
		return []settings.Field{metadata.Sensitive("webhook", "Custom webhook HTTPS URL"), metadata.Text("inputs", "JSON payload", "{}")}
	}
	f := []settings.Field{metadata.Device("scenario", "Scenario ID")}
	if a == "run" {
		f = append(f, metadata.Text("inputs", "Scenario JSON inputs", "{}"))
	}
	return f
}

var Factory = c.Factory(c.Options{Definition: Definition, Visuals: visuals, SharedKeys: metadata.Keys(PluginFields()), Interval: 30 * time.Second, New: func(chan<- struct{}) c.Driver {
	return &Driver{HTTP: c.NewHTTP()}
}})

type Scenario struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Active *bool  `json:"isActive"`
	TeamID int64  `json:"teamId"`
}
type Execution struct {
	Scenario  string
	Sequence  uint64
	ID, State string
	Terminal  bool
	Refresh   bool
}
type Sample struct {
	Pending   int
	Scenario  *Scenario
	Execution Execution
	Message   string
}

// Summarize the observed Make service test result
func (s Sample) TestResult() string {
	if strings.HasPrefix(s.Message, "Webhook") {
		return "Webhook configuration valid; no request was sent and execution was not tested"
	}
	return "Make API connection succeeded; scenario data read successfully"
}

type Driver struct {
	HTTP        *c.HTTP
	BaseURL     string
	Executions  *operations.Ledger[Execution]
	Webhooks    *operations.Ledger[string]
	mu          sync.Mutex
	active      map[string]bool
	demandKnown bool
}

// Choose the shared make resource identity for these settings
func (*Driver) Key(s map[string]any) string {
	if c.String(s, "_action", "") == "webhook" {
		sum := sha256.Sum256([]byte(c.String(s, "webhook", "")))
		return fmt.Sprintf("webhook:%x", sum)
	}
	return "scenario:" + scenarioIdentity(s)
}

// Validate the configured Make API base URL
func (d *Driver) base(s map[string]any) (string, error) {
	if d.BaseURL != "" {
		return d.BaseURL, nil
	}
	region := c.String(s, "region", "eu1")
	switch region {
	case "eu1", "eu2", "us1", "us2":
		return "https://" + region + ".make.com/api/v2", nil
	}
	return "", fmt.Errorf("unsupported Make region")
}

// Send a bounded make service request and decode its response
func (d *Driver) request(ctx context.Context, s map[string]any, method, path string, body, out any) error {
	base, err := d.base(s)
	if err != nil {
		return err
	}
	token, err := c.Secret(s, "token")
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("configure a Make API token")
	}
	return d.HTTP.JSON(ctx, method, base+path, map[string]string{"Authorization": "Token " + token, "Content-Type": "application/json"}, body, out)
}

// Validate a required Make identifier setting
func id(s map[string]any, key string) (string, error) {
	value := c.String(s, key, "")
	if len(value) > 128 {
		return "", fmt.Errorf("Make %s ID exceeds 128 bytes", key)
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return "", fmt.Errorf("select a valid Make %s ID", key)
	}
	return strconv.FormatInt(n, 10), nil
}

// Derive the scenario identity used to group operation history
func scenarioIdentity(s map[string]any) string {
	if key, err := id(s, "scenario"); err == nil {
		return key
	}
	return c.String(s, "scenario", "")
}

// Read the selected Make scenario metadata
func (d *Driver) scenario(ctx context.Context, s map[string]any) (*Scenario, error) {
	selected, err := id(s, "scenario")
	if err != nil {
		return nil, err
	}
	var result struct {
		Scenario *Scenario `json:"scenario"`
	}
	if err = d.request(ctx, s, "GET", "/scenarios/"+selected, nil, &result); err != nil {
		return nil, err
	}
	if result.Scenario == nil || result.Scenario.ID <= 0 || result.Scenario.Active == nil {
		return nil, fmt.Errorf("malformed Make scenario")
	}
	if strconv.FormatInt(result.Scenario.ID, 10) != selected {
		return nil, fmt.Errorf("Make scenario identity differs from selection")
	}
	team, err := id(s, "team_id")
	if err != nil {
		return nil, err
	}
	if result.Scenario.TeamID != 0 && strconv.FormatInt(result.Scenario.TeamID, 10) != team {
		return nil, fmt.Errorf("scenario belongs to another Make team")
	}
	return result.Scenario, nil
}

// Read the make resource selected by the binding settings
func (d *Driver) Read(ctx context.Context, s map[string]any) (any, error) {
	if c.String(s, "_action", "") == "webhook" {
		endpoint, err := c.Secret(s, "webhook")
		if err != nil {
			return nil, err
		}
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("configure a valid HTTPS custom webhook")
		}
		message := d.webhook(d.Key(s))
		if message == "" {
			message = "Webhook configured\nActivate to send"
		}
		return Sample{Message: message}, nil
	}
	if c.String(s, "scenario", "") == "" {
		_, err := d.Discover(ctx, s)
		return Sample{Message: "Connection succeeded · select a scenario"}, err
	}
	scenario, err := d.scenario(ctx, s)
	if err != nil {
		return nil, err
	}
	scenarioKey := strconv.FormatInt(scenario.ID, 10)
	latest := d.execution(scenarioKey)
	for _, receiptKey := range d.pendingExecutions(scenarioKey) {
		execution := d.executionReceipt(receiptKey)
		var response struct {
			Log *struct {
				ID            string `json:"id"`
				Status        *int   `json:"status"`
				DisplayStatus string `json:"displayStatus"`
				EventType     string `json:"eventType"`
			} `json:"scenarioLog"`
		}
		err = d.request(ctx, s, "GET", "/scenarios/"+strconv.FormatInt(scenario.ID, 10)+"/logs/"+execution.ID, nil, &response)
		if e, ok := err.(*c.HTTPError); ok && e.Status == 404 {
			execution.State = "Accepted · awaiting execution log"
			execution.Terminal = false
		} else if err != nil {
			return nil, err
		} else if response.Log == nil {
			return nil, fmt.Errorf("malformed Make execution log")
		} else {
			execution.State = "Running"
			execution.Terminal = false
			if response.Log.EventType == "EXECUTION_PAUSED" {
				execution.State = "Waiting"
			}
			if response.Log.DisplayStatus != "" {
				execution.State = response.Log.DisplayStatus
			}
			if response.Log.Status != nil {
				switch *response.Log.Status {
				case 1:
					execution.State = "Completed successfully"
					execution.Terminal = true
				case 2:
					execution.State = "Completed with warning"
					execution.Terminal = true
				case 3:
					execution.State = "Failed"
					execution.Terminal = true
				default:
					return nil, fmt.Errorf("unknown Make execution status")
				}
			}
		}
		execution.Refresh = false
		if len(execution.State) > executionStateBytes {
			return nil, fmt.Errorf("Make execution status exceeds 512 bytes")
		}
		d.storeExecution(receiptKey, execution)
		if execution.Sequence >= latest.Sequence {
			latest = execution
		}
	}
	return Sample{Scenario: scenario, Execution: latest, Pending: d.pendingCount(scenarioKey)}, nil
}

var executionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Execute the selected make command and report its outcome
func (d *Driver) Command(ctx context.Context, a string, s map[string]any, _ int) (c.Outcome, error) {
	if a == "status" || a == "execution" {
		d.refreshExecution(scenarioIdentity(s))
		return c.Outcome{Message: "Make status refresh requested"}, nil
	}
	if a == "webhook" {
		endpoint, err := c.Secret(s, "webhook")
		if err != nil {
			return c.Outcome{}, err
		}
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return c.Outcome{}, fmt.Errorf("webhook must use HTTPS")
		}
		body, err := c.JSONInputs(s, "inputs")
		if err != nil {
			return c.Outcome{}, err
		}
		data, _ := json.Marshal(body)
		key := d.Key(s)
		receiptKey, err := d.reserveWebhook(key)
		if err != nil {
			return c.Outcome{}, err
		}
		_, err = d.HTTP.Do(ctx, "POST", endpoint, map[string]string{"Content-Type": "application/json"}, data)
		if err != nil {
			d.storeWebhook(receiptKey, "Webhook delivery uncertain\nNo automatic retry")
			return c.Outcome{}, fmt.Errorf("webhook delivery uncertain; check Make before sending again")
		}
		message := "Webhook accepted\nExecution completion unknown"
		d.storeWebhook(receiptKey, message)
		return c.Outcome{Message: message}, nil
	}
	scenario, err := d.scenario(ctx, s)
	if err != nil {
		return c.Outcome{}, err
	}
	selected := strconv.FormatInt(scenario.ID, 10)
	path := "/scenarios/" + selected
	switch a {
	case "run":
		inputs, err := c.JSONInputs(s, "inputs")
		if err != nil {
			return c.Outcome{}, err
		}
		var response struct {
			ID string `json:"executionId"`
		}
		receiptKey, err := d.reserveExecution(selected)
		if err != nil {
			return c.Outcome{}, err
		}
		err = d.request(ctx, s, "POST", path+"/run", map[string]any{"data": inputs, "responsive": false}, &response)
		if err != nil {
			d.storeExecution(receiptKey, Execution{State: "Run delivery uncertain · no automatic retry"})
			return c.Outcome{}, fmt.Errorf("run delivery uncertain; check Make before submitting again")
		}
		if !executionID.MatchString(response.ID) {
			d.storeExecution(receiptKey, Execution{State: "Run accepted · execution ID unavailable"})
			return c.Outcome{}, fmt.Errorf("run response lacks a valid execution ID; check Make before submitting again")
		}
		d.storeExecution(receiptKey, Execution{ID: response.ID, State: "Accepted · completion pending"})
		return c.Outcome{Message: "Run accepted · completion pending"}, nil
	case "activate", "deactivate":
		suffix := "/start"
		if a == "deactivate" {
			suffix = "/stop"
		}
		err = d.request(ctx, s, "POST", path+suffix, nil, nil)
		if err != nil {
			return c.Outcome{}, fmt.Errorf("scenario change delivery uncertain; check Make before trying again")
		}
		return c.Outcome{Message: "Scenario change accepted; refreshing state"}, nil
	}
	return c.Outcome{}, fmt.Errorf("unknown Make action")
}

// List make choices for the property inspector
func (d *Driver) Discover(ctx context.Context, s map[string]any) ([]c.Choice, error) {
	team, err := id(s, "team_id")
	if err != nil {
		return nil, err
	}
	var choices []c.Choice
	bytes := 0
	for offset := 0; offset < 1000; offset += 100 {
		var response struct {
			Scenarios *[]Scenario `json:"scenarios"`
		}
		query := url.Values{"teamId": {team}, "pg[limit]": {"100"}, "pg[offset]": {strconv.Itoa(offset)}}
		if err = d.request(ctx, s, "GET", "/scenarios?"+query.Encode(), nil, &response); err != nil {
			return nil, err
		}
		if response.Scenarios == nil {
			return nil, fmt.Errorf("malformed Make scenario list")
		}
		if len(*response.Scenarios) > 100 {
			return nil, fmt.Errorf("Make returned more than 100 scenarios in one page; enter the scenario ID directly")
		}
		for _, scenario := range *response.Scenarios {
			if scenario.ID <= 0 || strings.TrimSpace(scenario.Name) == "" {
				return nil, fmt.Errorf("malformed Make scenario entry")
			}
			identifier := strconv.FormatInt(scenario.ID, 10)
			bytes += len(identifier) + len(scenario.Name) + len("scenario")
			if bytes > discoveryBytes {
				return nil, fmt.Errorf("Make scenario choices exceed 256 KiB; enter the scenario ID directly")
			}
			choices = append(choices, c.Choice{ID: identifier, Name: scenario.Name, Kind: "scenario"})
		}
		if len(*response.Scenarios) < 100 {
			return choices, nil
		}
	}
	return nil, fmt.Errorf("more than 1000 scenarios; enter the scenario ID directly")
}

// Format a make resource sample for its selected action
func (*Driver) Display(value any, a string, _ map[string]any) string {
	v := value.(Sample)
	if v.Message != "" {
		return v.Message
	}
	if v.Scenario == nil {
		return "Select a Make scenario"
	}
	if a == "run" || a == "execution" {
		if v.Execution.State == "" {
			return v.Scenario.Name + "\nNo retained run history"
		}
		return fmt.Sprintf("%s\n%s · %d pending", v.Scenario.Name, v.Execution.State, v.Pending)
	}
	active := "Inactive"
	if *v.Scenario.Active {
		active = "Active"
	}
	return v.Scenario.Name + "\n" + active
}

// Release the make driver resources during shutdown
func (d *Driver) Close() { d.HTTP.Close() }
