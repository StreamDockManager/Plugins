package engine

import "time"

// CollectSample coordinates cached status and job ownership. Acquisition, clock,
// cadence, retry, and specialized stale text remain the plugin's policies
func CollectSample(e *Engine, id, key string, previous SampledStatus, now func() time.Time, start func() bool, stale func(DisplayStatus) DisplayStatus) DisplayStatus {
	if previous.At.IsZero() {
		if sample, ok := e.Samples[key]; ok {
			previous = sample
			e.Statuses[id] = sample
		}
	}
	if !now().Before(previous.Next) {
		if _, busy := e.Jobs[key]; !busy {
			if !start() {
				previous.Next = now().Add(time.Second)
				e.Statuses[id] = previous
			} else {
				job := e.Jobs[key]
				job.Owner = id
				e.Jobs[key] = job
			}
		}
	}
	if previous.At.IsZero() {
		return DisplayStatus{Text: "Collecting…", Unavailable: true}
	}
	if now().Sub(previous.At) > max(3*time.Second, previous.Next.Sub(previous.At)*2) {
		return stale(previous.Status)
	}
	return previous.Status
}

// StaleSample marks a previously collected sample as stale
func StaleSample(status DisplayStatus) DisplayStatus {
	return DisplayStatus{Text: "Stale\n" + status.Text, Unavailable: true}
}

// CompleteSample fans a completed observation out to its current visible bindings
// Policy computes each binding's schedule; after runs only after all displays
// succeed. Failed display publication deliberately retains completion bookkeeping
func CompleteSample(e *Engine, key string, policy func(string, Binding, SampledStatus) SampledStatus, send func(string, DisplayStatus) error, after func()) error {
	for id, probe := range e.ProbeKeys {
		if probe != key {
			continue
		}
		binding, visible := e.Bindings[id]
		if !visible {
			continue
		}
		sample := policy(id, binding, e.Statuses[id])
		e.Statuses[id] = sample
		e.Samples[key] = sample
		if err := send(id, sample.Status); err != nil {
			return err
		}
	}
	if after != nil {
		after()
	}
	delete(e.ResultsText, key)
	delete(e.Checked, key)
	return nil
}
