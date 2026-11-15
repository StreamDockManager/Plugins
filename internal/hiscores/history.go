package hiscores

import (
	"slices"
	"sort"
)

const historyKeyLimit = 1024
const historyByteLimit = 4 << 20

type skillXP struct {
	ID int
	XP int64
}

// compactXP formats experience in a compact display label
func compactXP(sample Sample) []skillXP {
	count := 0
	for _, item := range sample.Skills {
		if item.XP != nil && *item.XP >= 0 {
			count++
		}
	}
	values := make([]skillXP, 0, count)
	for _, item := range sample.Skills {
		if item.XP != nil && *item.XP >= 0 {
			values = append(values, skillXP{item.ID, *item.XP})
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values
}

// priorXP looks up the prior retained experience sample
func priorXP(values []skillXP, id int) (int64, bool) {
	i := sort.Search(len(values), func(i int) bool { return values[i].ID >= id })
	if i == len(values) || values[i].ID != id {
		return 0, false
	}
	return values[i].XP, true
}

// Include both slices, keys and conservative map/record overhead. A reset shares
// the latest immutable values, so it releases rather than duplicates the prior
// baseline. This estimate is a cache policy, not a process-heap measurement
func historyBytes(key string, baseline, latest []skillXP) int {
	values := cap(baseline) + cap(latest)
	if len(baseline) > 0 && len(latest) > 0 && &baseline[0] == &latest[0] {
		values -= cap(baseline)
	}
	return 256 + 2*len(key) + 16*values
}

// historyFull reports whether the retained history has reached its bound
func historyFull() error {
	return &Failure{Label: "History full", Detail: "This Hiscores session reached its 1,024-player/board or 4 MiB history limit. Existing baselines are preserved. Reuse an existing player or restart the plugin to begin a new session."}
}

// retainXP records experience while enforcing history retention
func (d *Driver) retainXP(key string, sample Sample) ([]skillXP, error) {
	baseline, known := d.Baseline[key]
	if !known && len(d.Baseline) >= historyKeyLimit {
		return nil, historyFull()
	}
	latest := compactXP(sample)
	if prior, ok := d.Latest[key]; ok && slices.Equal(prior, latest) {
		latest = prior
	}
	oldBytes := 0
	if known {
		oldBytes = historyBytes(key, baseline, d.Latest[key])
	} else {
		baseline = latest
	}
	next := d.historyBytes - oldBytes + historyBytes(key, baseline, latest)
	if next > historyByteLimit {
		return nil, historyFull()
	}
	d.Baseline[key], d.Latest[key] = baseline, latest
	d.historyBytes = next
	return baseline, nil
}
