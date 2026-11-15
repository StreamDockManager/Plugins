//go:build darwin

package microphone

import (
	"context"
	"fmt"
	"github.com/ebitengine/purego"
	"sync"
	"unsafe"
)

type address struct {
	Selector uint32
	Scope    uint32
	Element  uint32
}

const input uint32 = 0x696e7074
const global uint32 = 0x676c6f62
const mute uint32 = 0x6d757465
const gain uint32 = 0x766f6c6d

var audio struct {
	once     sync.Once
	err      error
	get      func(uint32, *address, uint32, unsafe.Pointer, *uint32, unsafe.Pointer) int32
	set      func(uint32, *address, uint32, unsafe.Pointer, uint32, unsafe.Pointer) int32
	size     func(uint32, *address, uint32, unsafe.Pointer, *uint32) int32
	writable func(uint32, *address, *uint8) int32
	cfString func(uintptr, []byte, int64, uint32) bool
	release  func(uintptr)
}

// initialize initializes the owner state before use
func initialize() {
	audio.once.Do(func() {
		h, e := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if e != nil {
			audio.err = e
			return
		}
		purego.RegisterLibFunc(&audio.get, h, "AudioObjectGetPropertyData")
		purego.RegisterLibFunc(&audio.set, h, "AudioObjectSetPropertyData")
		purego.RegisterLibFunc(&audio.size, h, "AudioObjectGetPropertyDataSize")
		purego.RegisterLibFunc(&audio.writable, h, "AudioObjectIsPropertySettable")
		cf, e := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if e != nil {
			audio.err = e
			return
		}
		purego.RegisterLibFunc(&audio.cfString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&audio.release, cf, "CFRelease")
	})
}

type native struct {
	state string
	mu    sync.Mutex
	saved map[string]float32
}

// Native constructs the platform-native microphone adapter
func Native(state string) Adapter {
	initialize()
	n := &native{state: state, saved: map[string]float32{}}
	if saved, e := loadGains(state); e == nil {
		n.saved = saved
	}
	if n.saved == nil {
		n.saved = map[string]float32{}
	}
	return n
}

// property reads a Core Audio object property
func property(id, sel, scope, element uint32, out unsafe.Pointer, size uint32) error {
	if audio.err != nil {
		return audio.err
	}
	a := address{sel, scope, element}
	if s := audio.get(id, &a, 0, nil, &size, out); s != 0 {
		return fmt.Errorf("Core Audio property %x unavailable (OSStatus %d)", sel, s)
	}
	return nil
}

// text decodes a Core Audio string property
func text(id, sel uint32) (string, error) {
	var p uintptr
	if e := property(id, sel, global, 0, unsafe.Pointer(&p), 8); e != nil {
		return "", e
	}
	defer audio.release(p)
	b := make([]byte, 4096)
	if !audio.cfString(p, b, int64(len(b)), 0x08000100) {
		return "", fmt.Errorf("invalid Core Audio device string")
	}
	for i, c := range b {
		if c == 0 {
			return string(b[:i]), nil
		}
	}
	return "", fmt.Errorf("device name too long")
}

// Default returns the default input device
func (n *native) Default(ctx context.Context) (string, error) {
	var id uint32
	if e := property(1, 0x64496e20, global, 0, unsafe.Pointer(&id), 4); e != nil {
		return "", e
	}
	return text(id, 0x75696420)
}

// ids enumerates the Core Audio device identifiers
func (n *native) ids() ([]uint32, error) {
	if audio.err != nil {
		return nil, audio.err
	}
	a := address{0x64657623, global, 0}
	var size uint32
	if s := audio.size(1, &a, 0, nil, &size); s != 0 {
		return nil, fmt.Errorf("Core Audio enumeration failed: %d", s)
	}
	if size == 0 {
		return nil, nil
	}
	ids := make([]uint32, size/4)
	if s := audio.get(1, &a, 0, nil, &size, unsafe.Pointer(&ids[0])); s != 0 {
		return nil, fmt.Errorf("Core Audio enumeration failed: %d", s)
	}
	return ids, nil
}

// controls discovers the available microphone controls
func controls(id, selector uint32) []uint32 {
	out := []uint32{}
	for channel := uint32(0); channel <= 32; channel++ {
		a := address{selector, input, channel}
		var yes uint8
		if audio.writable(id, &a, &yes) == 0 && yes != 0 {
			out = append(out, channel)
			if channel == 0 {
				break
			}
		}
	}
	return out
}

// device reads the descriptor for one microphone device
func (n *native) device(id uint32) (Device, error) {
	uid, e := text(id, 0x75696420)
	if e != nil {
		return Device{}, e
	}
	name, e := text(id, 0x6c6e616d)
	if e != nil {
		return Device{}, e
	}
	d := Device{ID: uid, Name: name}
	cs := controls(id, mute)
	if len(cs) > 0 {
		d.Mode = "hardware mute"
		if gains := controls(id, gain); len(gains) > 0 {
			var value float32
			if property(id, gain, input, gains[0], unsafe.Pointer(&value), 4) == nil {
				d.Gain = value
			}
		}
		d.Muted = true
		for _, c := range cs {
			var v uint32
			if e = property(id, mute, input, c, unsafe.Pointer(&v), 4); e != nil {
				return d, e
			}
			d.Muted = d.Muted && v != 0
		}
		return d, nil
	}
	cs = controls(id, gain)
	if len(cs) > 0 {
		d.Mode = "software mute (input gain)"
		d.Muted = true
		for _, c := range cs {
			var v float32
			if e = property(id, gain, input, c, unsafe.Pointer(&v), 4); e != nil {
				return d, e
			}
			d.Gain = v
			d.Muted = d.Muted && v == 0
		}
		return d, nil
	}
	return d, fmt.Errorf("%s: no writable input mute or gain control", name)
}

// Devices lists the available microphone devices
func (n *native) Devices(ctx context.Context) ([]Device, error) {
	ids, e := n.ids()
	if e != nil {
		return nil, e
	}
	out := []Device{}
	for _, id := range ids {
		a := address{0x73746d23, input, 0}
		var size uint32
		if audio.size(id, &a, 0, nil, &size) != 0 || size == 0 {
			continue
		}
		if d, e := n.device(id); e == nil {
			out = append(out, d)
		} else if uid, x := text(id, 0x75696420); x == nil {
			name, _ := text(id, 0x6c6e616d)
			out = append(out, Device{ID: uid, Name: name, Mode: e.Error()})
		}
	}
	return out, nil
}

// find finds the microphone matching the requested identity
func (n *native) find(uid string) (uint32, error) {
	ids, e := n.ids()
	if e != nil {
		return 0, e
	}
	for _, id := range ids {
		if s, e := text(id, 0x75696420); e == nil && s == uid {
			return id, nil
		}
	}
	return 0, fmt.Errorf("selected microphone unavailable: %s", uid)
}

// Read reads the requested data through the owned adapter
func (n *native) Read(ctx context.Context, uid string) (Device, error) {
	id, e := n.find(uid)
	if e != nil {
		return Device{}, e
	}
	return n.device(id)
}

// persist writes retained state through the configured backend
func (n *native) persist() error { return saveGains(n.state, n.saved) }

// SetMute applies the requested microphone mute state
func (n *native) SetMute(ctx context.Context, uid string, m bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	id, e := n.find(uid)
	if e != nil {
		return e
	}
	selector := mute
	cs := controls(id, selector)
	software := len(cs) == 0
	if software {
		selector = gain
		cs = controls(id, selector)
	}
	if len(cs) == 0 {
		return fmt.Errorf("no writable input mute or gain control")
	}
	if software {
		// Save every channel before changing hardware, and fsync recovery state first
		if m {
			for _, c := range cs {
				var v float32
				if e = property(id, gain, input, c, unsafe.Pointer(&v), 4); e != nil {
					return e
				}
				key := fmt.Sprintf("%s/%d", uid, c)
				n.saved[key] = v
			}
			if e = n.persist(); e != nil {
				return e
			}
		}
		for _, c := range cs {
			var v float32
			if !m {
				var ok bool
				v, ok = n.saved[fmt.Sprintf("%s/%d", uid, c)]
				if !ok {
					return fmt.Errorf("no saved gain for this device; restore manually")
				}
			}
			a := address{gain, input, c}
			if s := audio.set(id, &a, 0, nil, 4, unsafe.Pointer(&v)); s != 0 {
				return fmt.Errorf("input gain write failed (OSStatus %d)", s)
			}
		}
	} else {
		var v uint32
		if m {
			v = 1
		}
		for _, c := range cs {
			a := address{mute, input, c}
			if s := audio.set(id, &a, 0, nil, 4, unsafe.Pointer(&v)); s != 0 {
				return fmt.Errorf("input mute write failed (OSStatus %d)", s)
			}
		}
	}
	return nil
}
