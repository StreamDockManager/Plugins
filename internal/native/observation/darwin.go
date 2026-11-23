//go:build darwin

// Package observation reuses native status text while the OS reports no changes.
package observation

import (
	"context"
	"github.com/ebitengine/purego"
	"sync"
)

// Each plugin owns one monitor. Registration, checking, and publishing the text
// are serialized so a concurrent read cannot consume another caller's change.
type Monitor struct {
	mu    sync.Mutex
	check func() (bool, bool)
	text  string
	valid bool
}

func (m *Monitor) Read(ctx context.Context, open func() func() (bool, bool), read func(context.Context) (string, error)) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m.check == nil {
		m.check = open()
	}
	changed, reliable := m.check()
	if reliable && !changed && m.valid && ctx.Value(refreshKey{}) != true {
		return m.text, nil
	}
	m.valid = false
	text, err := read(ctx)
	if err == nil && reliable {
		m.text, m.valid = text, true
	}
	return text, err
}

func unavailable() (bool, bool) { return true, false }

func bind[T any](lib uintptr, symbol string, fn *T) bool {
	address, err := purego.Dlsym(lib, symbol)
	if err != nil {
		return false
	}
	purego.RegisterFunc(fn, address)
	return true
}

func PowerChanges() func() (bool, bool) {
	var register func(string, *int32) uint32
	var check func(int32, *int32) uint32
	if !bind(purego.RTLD_DEFAULT, "notify_register_check", &register) || !bind(purego.RTLD_DEFAULT, "notify_check", &check) {
		return unavailable
	}
	var token int32
	if register("com.apple.system.powersources", &token) != 0 {
		return unavailable
	}
	return func() (bool, bool) {
		var changed int32
		status := check(token, &changed)
		return changed != 0, status == 0
	}
}

func NetworkChanges() func() (bool, bool) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/SystemConfiguration.framework/SystemConfiguration", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return unavailable
	}
	var cfString func(uintptr, string, uint32) uintptr
	var array func(uintptr, int, uintptr) uintptr
	var appendValue func(uintptr, uintptr)
	var count func(uintptr) int
	var release func(uintptr)
	var create func(uintptr, uintptr, uintptr, uintptr) uintptr
	var subscribe func(uintptr, uintptr, uintptr) uint8
	var pending func(uintptr) uintptr
	if !bind(lib, "CFStringCreateWithCString", &cfString) || !bind(lib, "CFArrayCreateMutable", &array) || !bind(lib, "CFArrayAppendValue", &appendValue) ||
		!bind(lib, "CFArrayGetCount", &count) || !bind(lib, "CFRelease", &release) || !bind(lib, "SCDynamicStoreCreate", &create) ||
		!bind(lib, "SCDynamicStoreSetNotificationKeys", &subscribe) || !bind(lib, "SCDynamicStoreCopyNotifiedKeys", &pending) {
		return unavailable
	}
	const utf8 = 0x08000100
	name := cfString(0, "Stream Dock VPN", utf8)
	if name == 0 {
		return unavailable
	}
	defer release(name)
	store := create(0, name, 0, 0)
	if store == 0 {
		return unavailable
	}
	patterns := array(0, 0, 0)
	if patterns == 0 {
		release(store)
		return unavailable
	}
	defer release(patterns)
	for _, pattern := range []string{"Setup:/Network/.*", "State:/Network/Service/.*"} {
		key := cfString(0, pattern, utf8)
		if key == 0 {
			release(store)
			return unavailable
		}
		defer release(key)
		appendValue(patterns, key)
	}
	if subscribe(store, 0, patterns) == 0 {
		release(store)
		return unavailable
	}
	return func() (bool, bool) {
		keys := pending(store)
		if keys == 0 {
			return true, false
		}
		defer release(keys)
		return count(keys) > 0, true
	}
}
