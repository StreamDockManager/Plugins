package hiscores

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type historyTransport func(*http.Request) (*http.Response, error)

// RoundTrip returns the deterministic HTTP fixture response
func (f historyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// historyResponse constructs the deterministic hiscores history response
func historyResponse(skills int, xp int64) string {
	var body strings.Builder
	body.WriteString(`{"skills":[`)
	for i := range skills {
		if i > 0 {
			body.WriteByte(',')
		}
		name := fmt.Sprintf("Fixture skill %d", i)
		if i == 0 {
			name = "Overall"
		}
		fmt.Fprintf(&body, `{"id":%d,"name":%q,"rank":1,"level":99,"xp":%d}`, i, name, xp)
	}
	body.WriteString(`],"activities":[`)
	for i := range 64 {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"id":%d,"name":%q,"rank":1,"score":100}`, i, strings.Repeat("x", 128))
	}
	body.WriteString(`]}`)
	return body.String()
}

// TestHiscoresHistoryPlateauPreservesEveryBaselineAndReset verifies hiscores history plateau preserves every baseline and reset
func TestHiscoresHistoryPlateauPreservesEveryBaselineAndReset(t *testing.T) {
	calls := int64(0)
	d := New(map[string]string{"normal": "https://fixture.invalid/hiscores"}, nil)
	d.HTTP.Client = &http.Client{Transport: historyTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(historyResponse(32, 9007199254740993+calls)))}, nil
	})}
	defer d.Close()
	for i := range historyKeyLimit + 100 {
		s := map[string]any{"username": fmt.Sprintf("Fixture%04d", i)}
		for range 2 {
			value, err := d.Read(context.Background(), s)
			if i < historyKeyLimit {
				if err != nil || value.(Sample).Skills["0"].Name != "Overall" {
					t.Fatal(value, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "History full") {
				t.Fatal("new history was not rejected", err)
			}
		}
	}
	if calls != historyKeyLimit*2 || len(d.Baseline) != historyKeyLimit || d.historyBytes > historyByteLimit {
		t.Fatal("history admission leaked", calls, len(d.Baseline), d.historyBytes)
	}
	before := d.historyBytes
	for i := range historyKeyLimit {
		key := d.Key(map[string]any{"username": fmt.Sprintf("Fixture%04d", i)})
		if xp, ok := priorXP(d.Baseline[key], 0); !ok || xp != 9007199254740994+int64(2*i) {
			t.Fatal("prior baseline changed", key, xp)
		}
	}
	first := map[string]any{"username": "Fixture0000"}
	value, err := d.Read(context.Background(), first)
	if err != nil || value.(Sample).Delta["0"] != historyKeyLimit*2 {
		t.Fatal("existing player no longer works at capacity", value, err)
	}
	count := calls
	if _, err = d.Command(context.Background(), "reset-baseline", first, 0); err != nil || calls != count || d.historyBytes >= before {
		t.Fatal("reset changed request/retention semantics", err)
	}
	value, err = d.Read(context.Background(), first)
	if err != nil || value.(Sample).Delta["0"] != 1 {
		t.Fatal("reset did not use latest XP", value, err)
	}
	t.Logf("1024 accepted keys +100 rejected: estimated_bytes=%d retained_XP_values=%d HTTP_calls_before_revisit=%d", before, historyKeyLimit*64, count-1)
}

// TestHiscoresByteBudgetRejectsWithoutChangingLatestOrBaseline verifies hiscores byte budget rejects without changing latest or baseline
func TestHiscoresByteBudgetRejectsWithoutChangingLatestOrBaseline(t *testing.T) {
	d := New(nil, nil)
	defer d.Close()
	sample, err := ParseJSON([]byte(historyResponse(2000, 123)))
	if err != nil {
		t.Fatal(err)
	}
	admitted := 0
	for i := range historyKeyLimit {
		if _, err = d.retainXP(fmt.Sprint(i), sample); err != nil {
			break
		}
		admitted++
	}
	if admitted == 0 || admitted == historyKeyLimit || err == nil || d.historyBytes > historyByteLimit {
		t.Fatal("byte limit ignored", admitted, err, d.historyBytes)
	}
	before := d.historyBytes
	if _, err = d.retainXP("0", sample); err != nil || d.historyBytes != before {
		t.Fatal("unchanged old player rejected", err)
	}
	changed, err := ParseJSON([]byte(historyResponse(2000, 456)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.retainXP("0", changed); err == nil {
		t.Fatal("oversized replacement admitted")
	}
	if d.historyBytes != before {
		t.Fatal("rejected sample changed accounting")
	}
	if xp, _ := priorXP(d.Latest["0"], 0); xp != 123 {
		t.Fatal("rejected sample changed latest XP")
	}
	if xp, _ := priorXP(d.Baseline["0"], 0); xp != 123 {
		t.Fatal("rejected sample changed baseline")
	}
}
