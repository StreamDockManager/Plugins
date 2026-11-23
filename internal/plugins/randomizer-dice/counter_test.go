package randomizerdice

import (
	"context"
	"encoding/json"
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"github.com/StreamDockManager/Plugin-SDK/Go/testkit"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDiceRollCounterAggregatePersistenceAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	e := New(path)
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	send := func(kind, id, a string, s map[string]any) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(kind, id, a, s)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"counter-a", "counter-b"} {
		send("willAppear", id, "dice-roll-counter", nil)
	}
	if e.Last["counter-a"].Title != "0" || e.Last["counter-b"].Title != "0" {
		t.Fatal("counter not initially visible zero")
	}
	for i, sides := range []int{2, 20, 1000000} {
		settings := map[string]any{"sides": sides, "show_result_badge": false}
		send("willAppear", "die", "dice", settings)
		send("keyDown", "die", "dice", settings)
		if e.lookupState(counterStateKey, nil).RollCount != uint64(i+1) {
			t.Fatal("not one per accepted roll")
		}
		if e.Last["counter-a"].Title != e.counterStatus().Text || e.Last["counter-b"].Title != e.counterStatus().Text {
			t.Fatal("counter fanout missing")
		}
	}
	result := e.State["die"].Value
	clock.Advance(2 * time.Second)
	if err := e.Poll(ctx, host.Client); err != nil {
		t.Fatal(err)
	}
	send("willAppear", "die", "dice", nil)
	for _, a := range []string{"range", "list", "draw", "magic-8-ball", "reset"} {
		send("keyDown", "other", a, nil)
	}
	if e.lookupState(counterStateKey, nil).RollCount != 3 {
		t.Fatal("non-roll increment")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e = New(path)
	defer e.Close()
	e.Now = clock.Now
	if e.lookupState(counterStateKey, nil).RollCount != 3 {
		t.Fatal("count lost on restart")
	}
	for _, id := range []string{"counter-a", "counter-b"} {
		send("willAppear", id, "dice-roll-counter", nil)
	}
	send("keyDown", "counter-b", "dice-roll-counter", nil)
	if e.Last["counter-a"].Title != "0" || e.Last["counter-b"].Title != "0" || e.State["die"].Value != result {
		t.Fatal("reset changed result or failed fanout")
	}
	send("keyDown", "die", "dice", map[string]any{"sides": 6})
	send("keyDown", "die", "dice", map[string]any{"sides": 6})
	if e.lookupState(counterStateKey, nil).RollCount != 2 {
		t.Fatal("fast rerolls not counted")
	}
}

func TestDiceTotalCounterAndCountAreIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	e := New(path)
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	send := func(kind, id, a string, s map[string]any) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(kind, id, a, s)); err != nil {
			t.Fatal(err)
		}
	}
	showCounters := func() {
		send("willAppear", "count", countCounterAction, nil)
		send("willAppear", "total-a", totalCounterAction, nil)
		send("willAppear", "total-b", totalCounterAction, nil)
	}
	checkCounters := func(count, total uint64) {
		t.Helper()
		got := e.lookupState(counterStateKey, nil)
		if got.RollCount != count || e.totalCounterStatus().Text != strconv.FormatUint(total, 10) {
			t.Fatalf("aggregates = %d/%s; want %d/%d", got.RollCount, e.totalCounterStatus().Text, count, total)
		}
		if e.Last["count"].Title != strconv.FormatUint(count, 10) ||
			e.Last["total-a"].Title != strconv.FormatUint(total, 10) ||
			e.Last["total-b"].Title != strconv.FormatUint(total, 10) {
			t.Fatalf("visible counter mismatch: count=%q totals=%q,%q; want %d/%d", e.Last["count"].Title, e.Last["total-a"].Title, e.Last["total-b"].Title, count, total)
		}
	}
	showCounters()
	checkCounters(0, 0)
	var total uint64
	for i, sides := range []int{2, 20, 1_000_000, 1_000_000} {
		settings := map[string]any{"sides": sides, "show_result_badge": false}
		send("willAppear", "die", "dice", settings)
		send("keyDown", "die", "dice", settings)
		total += uint64(e.State["die"].Value)
		checkCounters(uint64(i+1), total-uint64(e.State["die"].Value))
		clock.Advance(rollDuration)
		if err := e.Poll(ctx, host.Client); err != nil {
			t.Fatal(err)
		}
		checkCounters(uint64(i+1), total)
	}
	result := e.State["die"].Value
	for _, a := range []string{"range", "list", "draw", "magic-8-ball", "reset"} {
		send("keyDown", "other", a, nil)
	}
	checkCounters(4, total)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e = New(path)
	e.Now = clock.Now
	defer e.Close()
	showCounters()
	checkCounters(4, total)
	send("keyDown", "total-a", totalCounterAction, nil)
	checkCounters(4, 0)
	if e.State["die"].Value != result {
		t.Fatal("total reset changed last roll")
	}
	send("keyDown", "die", "dice", nil)
	result = e.State["die"].Value
	send("keyDown", "count", countCounterAction, nil)
	checkCounters(0, 0)
	clock.Advance(time.Duration(badgeFinalFrame) * rollFrameInterval)
	if err := e.Poll(ctx, host.Client); err != nil {
		t.Fatal(err)
	}
	checkCounters(0, uint64(result))
	if e.State["die"].Value != result {
		t.Fatal("counter reset changed the last roll")
	}
}

func TestDiceTotalOverflowRejectsWithoutChangingRoll(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	if err := e.Handle(ctx, host.Client, randomizerEvent("willAppear", "total", totalCounterAction, nil)); err != nil {
		t.Fatal(err)
	}
	e.State[counterStateKey] = &saved{RollCount: 42, RollTotal: math.MaxUint64}
	e.State["die"] = &saved{Value: 5}
	state := e.State["die"]
	if err := e.Handle(ctx, host.Client, randomizerEvent("keyDown", "die", "dice", nil)); err == nil || !strings.Contains(err.Error(), "dice total counter is full") {
		t.Fatalf("expected overflow rejection, got %v", err)
	}
	if e.State["die"] != state || state.Value != 5 || e.State[counterStateKey].RollCount != 42 ||
		e.State[counterStateKey].RollTotal != math.MaxUint64 || len(e.rolls) != 0 || e.DirtyState {
		t.Fatal("overflow modified a saved counter, result, or animation")
	}
}

// Verify multiple dice settle independently and total feedback follows the completed badge
func TestDiceTotalWaitsForEachReveal(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	send := func(kind, id, name string, settings map[string]any) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(kind, id, name, settings)); err != nil {
			t.Fatal(err)
		}
	}
	tick := func(delay time.Duration) {
		t.Helper()
		clock.Advance(delay)
		if err := e.Poll(ctx, host.Client); err != nil {
			t.Fatal(err)
		}
	}
	send("willAppear", "total", totalCounterAction, nil)
	send("keyDown", "d20", "dice", map[string]any{"sides": 20})
	first := uint64(e.State["d20"].Value)
	tick(rollFrameInterval)
	send("keyDown", "d10", "dice", map[string]any{"sides": 10})
	second := uint64(e.State["d10"].Value)
	tick(time.Duration(badgeFinalFrame-2) * rollFrameInterval)
	if e.Last["total"].Title != "0" {
		t.Fatal("total exposed a roll before the badge finished")
	}
	host.Drain()
	tick(rollFrameInterval)
	messages := host.Drain()
	revealIndex, totalIndex := -1, -1
	for i, ev := range messages {
		if ev.Event == "setImage" && ev.Context == "d20" {
			revealIndex = i
		}
		if ev.Event == "setTitle" && ev.Context == "total" {
			totalIndex = i
		}
	}
	if revealIndex < 0 || totalIndex <= revealIndex || e.Last["total"].Title != strconv.FormatUint(first, 10) {
		t.Fatalf("total was not sent after the first reveal: reveal=%d total=%d", revealIndex, totalIndex)
	}
	tick(rollFrameInterval)
	if e.Last["total"].Title != strconv.FormatUint(first+second, 10) {
		t.Fatal("second roll did not update total after its own reveal")
	}
}

// Verify rerolls preserve each accepted result while a reset discards any held values
func TestDiceTotalRerollAndResetDuringAnimation(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	send := func(event, id, action string) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(event, id, action, nil)); err != nil {
			t.Fatal(err)
		}
	}
	tick := func(delay time.Duration) {
		t.Helper()
		clock.Advance(delay)
		if err := e.Poll(ctx, host.Client); err != nil {
			t.Fatal(err)
		}
	}
	duration := time.Duration(badgeFinalFrame) * rollFrameInterval
	send("willAppear", "total", totalCounterAction)
	send("keyDown", "die", "dice")
	first := uint64(e.State["die"].Value)
	tick(rollFrameInterval)
	send("keyDown", "die", "dice")
	second := uint64(e.State["die"].Value)
	send("willAppear", "total", totalCounterAction)
	tick(duration - rollFrameInterval)
	if e.Last["total"].Title != "0" {
		t.Fatal("reroll or newly shown counter exposed a pending value")
	}
	tick(rollFrameInterval)
	if e.Last["total"].Title != strconv.FormatUint(first+second, 10) {
		t.Fatal("reroll lost an accepted value")
	}

	send("keyDown", "die", "dice")
	send("keyDown", "total", totalCounterAction)
	tick(duration)
	if e.Last["total"].Title != "0" || e.lookupState(counterStateKey, nil).RollTotal != 0 {
		t.Fatal("a pending animation undid the reset")
	}
}

// Verify a hidden die no longer animates but its accepted roll is still counted at the reveal deadline
func TestDiceTotalRetainsHiddenRoll(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	clock := testkit.NewClock(time.Now())
	e.Now = clock.Now
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	send := func(event, id, action string) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(event, id, action, nil)); err != nil {
			t.Fatal(err)
		}
	}
	send("willAppear", "total", totalCounterAction)
	send("keyDown", "die", "dice")
	send("willDisappear", "die", "dice")
	send("willAppear", "total", totalCounterAction)
	if len(e.rolls) != 0 || e.Last["total"].Title != "0" {
		t.Fatal("hiding the die continued animation or exposed the result")
	}
	clock.Advance(time.Duration(badgeFinalFrame) * rollFrameInterval)
	if err := e.Poll(ctx, host.Client); err != nil {
		t.Fatal(err)
	}
	if e.Last["total"].Title != strconv.FormatFloat(e.State["die"].Value, 'f', 0, 64) {
		t.Fatal("hiding the die lost an accepted roll")
	}
}

// Verify saved assignments control tracking and a confirmed removal clears only that counter
func TestConfiguredCountersSkipUnassignedAndClearOnRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	e := New(path)
	host := testkit.New(sdk.HostInfo{})
	ctx := context.Background()
	send := func(kind, id, name string) {
		t.Helper()
		if err := e.Handle(ctx, host.Client, randomizerEvent(kind, id, name, nil)); err != nil {
			t.Fatal(err)
		}
	}
	configure := func(names []string) {
		t.Helper()
		payload, err := json.Marshal(map[string][]string{"actions": names})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Handle(ctx, host.Client, sdk.Event{Event: configuredActionsEvent, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(count, total uint64) {
		t.Helper()
		a := e.lookupState(counterStateKey, nil)
		if a.RollCount != count || a.RollTotal != total {
			t.Fatalf("got %d rolls and total %d, want %d and %d", a.RollCount, a.RollTotal, count, total)
		}
	}

	configure([]string{})
	send("keyDown", "die", "dice")
	check(0, 0)
	if e.State[counterStateKey] != nil || len(e.heldTotals) != 0 {
		t.Fatal("unassigned counters wrote state")
	}
	configure([]string{countCounterAction, totalCounterAction})
	send("willAppear", "count", countCounterAction)
	send("willDisappear", "count", countCounterAction)
	send("keyDown", "die", "dice")
	value := uint64(e.State["die"].Value)
	check(1, value)
	if e.totalCounterStatus().Text != "0" {
		t.Fatal("total revealed early")
	}
	configure([]string{countCounterAction})
	check(1, 0)
	if len(e.heldTotals) != 0 {
		t.Fatal("removing total did not discard its pending reveal")
	}
	send("keyDown", "die", "dice")
	check(2, 0)
	configure([]string{totalCounterAction})
	check(0, 0)
	send("keyDown", "die", "dice")
	check(0, uint64(e.State["die"].Value))
	configure([]string{})
	check(0, 0)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e = New(path)
	defer e.Close()
	check(0, 0)
}

// Verify a portable host counts only shown counter actions without treating navigation as deletion
func TestPortableCountersTrackVisibleActionsIndependently(t *testing.T) {
	e := New(filepath.Join(t.TempDir(), "state.json"))
	defer e.Close()
	host := testkit.New(sdk.HostInfo{})
	send := func(event, id, action string) {
		t.Helper()
		if err := e.Handle(context.Background(), host.Client, randomizerEvent(event, id, action, nil)); err != nil {
			t.Fatal(err)
		}
	}
	send("willAppear", "count-a", countCounterAction)
	send("willAppear", "count-b", countCounterAction)
	send("keyDown", "die", "dice")
	send("willDisappear", "count-a", countCounterAction)
	send("keyDown", "die", "dice")
	send("willDisappear", "count-b", countCounterAction)
	send("keyDown", "die", "dice")
	aggregate := e.lookupState(counterStateKey, nil)
	if aggregate.RollCount != 2 || aggregate.RollTotal != 0 || len(e.heldTotals) != 0 {
		t.Fatal("unavailable counter tracked a roll or navigation cleared the count")
	}
}
