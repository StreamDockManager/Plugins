package makeplugin

import (
	"fmt"
	operations "github.com/StreamDockManager/Plugin-SDK/Go/operations"
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"strings"
)

const receiptLimit = 1024
const executionStateBytes = 512
const discoveryBytes = 256 << 10
const receiptCost = 1024

// The SDK owns admission, ordering and terminal retirement. Make owns the meaning
// of its service responses; HTTP acceptance alone never establishes completion
func (d *Driver) history() {
	if d.Executions == nil {
		d.Executions, _ = operations.New[Execution](receiptLimit, 2<<20)
	}
	if d.Webhooks == nil {
		d.Webhooks, _ = operations.New[string](receiptLimit, 2<<20)
	}
}

// Retain make resources required by the current bindings
func (d *Driver) Demand(bindings []map[string]any) {
	active := map[string]bool{}
	for _, s := range bindings {
		if c.String(s, "_action", "") != "webhook" {
			if scenario := scenarioIdentity(s); scenario != "" {
				active[scenario] = true
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.history()
	d.active, d.demandKnown = active, true
	d.Executions.Retire(active)
}

// Read the newest retained execution for a scenario
func (d *Driver) execution(scenario string) Execution {
	d.mu.Lock()
	defer d.mu.Unlock()
	records := d.Executions.Records(scenario)
	if len(records) == 0 {
		return Execution{}
	}
	return records[len(records)-1].Value
}

// List scenario execution receipts still requiring observation
func (d *Driver) pendingExecutions(scenario string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := []string{}
	for _, r := range d.Executions.Records(scenario) {
		if r.Value.ID != "" && (!r.Value.Terminal || r.Value.Refresh) {
			keys = append(keys, r.ID)
		}
	}
	return keys
}

// Read the retained execution identified by a receipt key
func (d *Driver) executionReceipt(key string) Execution {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, _ := d.Executions.Get(key)
	return r.Value
}

// Count pending and uncertain scenario operations
func (d *Driver) pendingCount(scenario string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, r := range d.Executions.Records(scenario) {
		if r.State == operations.Pending || r.State == operations.Uncertain {
			count++
		}
	}
	return count
}

// Update a reserved execution without treating HTTP acceptance as completion
func (d *Driver) storeExecution(key string, record Execution) {
	d.mu.Lock()
	defer d.mu.Unlock()
	prior, ok := d.Executions.Get(key)
	if !ok {
		panic("Make execution was not reserved")
	}
	record.Scenario, record.Sequence = prior.Group, prior.Sequence
	state := operations.Pending
	if record.Terminal {
		state = operations.Confirmed
		if record.State == "Failed" {
			state = operations.Failed
		}
	} else if record.ID == "" {
		state = operations.Uncertain
	}
	if err := d.Executions.Update(key, state, record, receiptCost, record.Refresh); err != nil {
		panic(err)
	}
	if d.demandKnown {
		d.Executions.Retire(d.active)
	}
}

// Reserve history capacity before submitting a scenario run
func (d *Driver) reserveExecution(scenario string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.history()
	// Reserve receipt capacity before any service call can submit a command
	r, err := d.Executions.Reserve(scenario, Execution{State: "Run submission pending"}, receiptCost)
	if err != nil {
		return "", fmt.Errorf("Make execution history is full; remove completed actions or check Make before restarting the plugin. No new run was submitted")
	}
	value := r.Value
	value.Scenario, value.Sequence = r.Group, r.Sequence
	if err = d.Executions.Update(r.ID, operations.Pending, value, receiptCost, false); err != nil {
		return "", err
	}
	return r.ID, nil
}

// Mark retained scenario executions for explicit refresh
func (d *Driver) refreshExecution(scenario string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.Executions.Records(scenario) {
		value := r.Value
		value.Refresh = true
		_ = d.Executions.Update(r.ID, r.State, value, receiptCost, true)
	}
}

// Read the most recent webhook receipt message
func (d *Driver) webhook(key string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	records := d.Webhooks.Records(key)
	if len(records) == 0 {
		return ""
	}
	return records[len(records)-1].Value
}

// Reserve receipt capacity before sending a webhook
func (d *Driver) reserveWebhook(key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.history()
	r, err := d.Webhooks.Reserve(key, "Webhook submission pending", receiptCost)
	if err != nil {
		return "", fmt.Errorf("Make webhook receipt history is full; check Make before restarting the plugin. No webhook was sent")
	}
	return r.ID, nil
}

// Record the outcome for a previously reserved webhook
func (d *Driver) storeWebhook(key, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := operations.Uncertain
	if strings.Contains(message, "submission pending") {
		state = operations.Pending
	}
	if err := d.Webhooks.Update(key, state, message, receiptCost, false); err != nil {
		panic(err)
	}
}
