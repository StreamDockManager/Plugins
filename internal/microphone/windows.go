//go:build windows

package microphone

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

var enumeratorClass = guid{0xbcde0395, 0xe52f, 0x467c, [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
var enumeratorIID = guid{0xa95664d2, 0x9614, 0x4f35, [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
var volumeIID = guid{0x5cdf2c82, 0x841e, 0x4546, [8]byte{0x97, 0x22, 0x0c, 0xf7, 0x40, 0x78, 0x22, 0x9a}}
var ole = syscall.NewLazyDLL("ole32.dll")
var coinit = ole.NewProc("CoInitializeEx")
var couninit = ole.NewProc("CoUninitialize")
var create = ole.NewProc("CoCreateInstance")
var free = ole.NewProc("CoTaskMemFree")
var clear = ole.NewProc("PropVariantClear")

type object struct{ v *[32]uintptr }

// call invokes the selected native interface method
func call(o *object, index int, args ...uintptr) (uintptr, error) {
	if o == nil {
		return 0, fmt.Errorf("Core Audio object unavailable")
	}
	a := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	h, _, _ := syscall.SyscallN(o.v[index], a...)
	if int32(h) < 0 {
		return h, fmt.Errorf("Core Audio access failed (HRESULT 0x%x)", uint32(h))
	}
	return h, nil
}

// release releases the native interface reference
func release(o *object) {
	if o != nil {
		_, _ = call(o, 2)
	}
}

// apartment establishes the COM apartment used by this operation
func apartment(fn func(*object) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coinit.Call(0, 0)
	if int32(hr) < 0 {
		return fmt.Errorf("COM initialization: 0x%x", hr)
	}
	defer couninit.Call()
	var e *object
	hr, _, _ = create.Call(uintptr(unsafe.Pointer(&enumeratorClass)), 0, 23, uintptr(unsafe.Pointer(&enumeratorIID)), uintptr(unsafe.Pointer(&e)))
	if int32(hr) < 0 {
		return fmt.Errorf("Core Audio enumerator unavailable: 0x%x", hr)
	}
	defer release(e)
	return fn(e)
}

// wide encodes a string for a Windows native call
func wide(p *uint16) string {
	if p == nil {
		return ""
	}
	s := unsafe.Slice(p, 32768)
	n := 0
	for n < len(s) && s[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(s[:n])
}

// deviceID reads the native endpoint identity
func deviceID(d *object) (string, error) {
	var p *uint16
	_, e := call(d, 5, uintptr(unsafe.Pointer(&p)))
	if e != nil {
		return "", e
	}
	defer free.Call(uintptr(unsafe.Pointer(p)))
	return wide(p), nil
}

// name reads the endpoint display name
func name(d *object, id string) string {
	var store *object
	if _, e := call(d, 4, 0, uintptr(unsafe.Pointer(&store))); e != nil {
		return id
	}
	defer release(store)
	key := struct {
		G  guid
		ID uint32
	}{guid{0xa45c254e, 0xdf1c, 0x4efd, [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, 14}
	var value [24]byte
	if _, e := call(store, 5, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&value))); e != nil {
		return id
	}
	defer clear.Call(uintptr(unsafe.Pointer(&value)))
	if *(*uint16)(unsafe.Pointer(&value[0])) == 31 {
		return wide(*(**uint16)(unsafe.Pointer(&value[8])))
	}
	return id
}

// endpoint resolves the selected Windows audio endpoint
func endpoint(e *object, id string) (*object, error) {
	p, err := syscall.UTF16PtrFromString(id)
	if err != nil {
		return nil, err
	}
	var d *object
	_, err = call(e, 5, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&d)))
	return d, err
}

// volume opens the endpoint volume interface
func volume(d *object) (*object, error) {
	var v *object
	_, e := call(d, 3, uintptr(unsafe.Pointer(&volumeIID)), 23, 0, uintptr(unsafe.Pointer(&v)))
	return v, e
}

// read reads the current native endpoint state
func read(d *object) (Device, error) {
	id, e := deviceID(d)
	if e != nil {
		return Device{}, e
	}
	v, e := volume(d)
	if e != nil {
		return Device{}, e
	}
	defer release(v)
	var muted int32
	_, e = call(v, 15, uintptr(unsafe.Pointer(&muted)))
	var scalar float32
	if e == nil {
		_, e = call(v, 9, uintptr(unsafe.Pointer(&scalar)))
	}
	return Device{ID: id, Name: name(d, id), Muted: muted != 0, Mode: "hardware mute", Gain: scalar}, e
}

type native struct{}

// Native constructs the platform-native microphone adapter
func Native(state string) Adapter { return native{} }

// Default returns the default input device
func (n native) Default(ctx context.Context) (id string, err error) {
	err = apartment(func(e *object) error {
		var d *object
		_, x := call(e, 4, 1, 0, uintptr(unsafe.Pointer(&d)))
		if x != nil {
			return x
		}
		defer release(d)
		id, x = deviceID(d)
		return x
	})
	return
}

// Devices lists the available microphone devices
func (n native) Devices(ctx context.Context) (out []Device, err error) {
	err = apartment(func(e *object) error {
		var collection *object
		if _, x := call(e, 3, 1, 1, uintptr(unsafe.Pointer(&collection))); x != nil {
			return x
		}
		defer release(collection)
		var count uint32
		if _, x := call(collection, 3, uintptr(unsafe.Pointer(&count))); x != nil {
			return x
		}
		for i := uint32(0); i < count; i++ {
			var d *object
			if _, x := call(collection, 4, uintptr(i), uintptr(unsafe.Pointer(&d))); x != nil {
				return x
			}
			v, x := read(d)
			release(d)
			if x != nil {
				return x
			}
			out = append(out, v)
		}
		return nil
	})
	return
}

// Read reads the requested data through the owned adapter
func (n native) Read(ctx context.Context, id string) (out Device, err error) {
	err = apartment(func(e *object) error {
		d, x := endpoint(e, id)
		if x != nil {
			return x
		}
		defer release(d)
		if x = captureOnly(d); x != nil {
			return x
		}
		out, x = read(d)
		return x
	})
	return
}

// SetMute applies the requested microphone mute state
func (n native) SetMute(ctx context.Context, id string, m bool) error {
	return apartment(func(e *object) error {
		d, x := endpoint(e, id)
		if x != nil {
			return x
		}
		defer release(d)
		if x = captureOnly(d); x != nil {
			return x
		}
		v, x := volume(d)
		if x != nil {
			return x
		}
		defer release(v)
		var flag uintptr
		if m {
			flag = 1
		}
		_, x = call(v, 14, flag, 0)
		return x
	})
}

// ApplicationVolume uses supported Core Audio render-session APIs, never capture endpoints
func ApplicationVolume(ctx context.Context, application string, gain float32) (string, error) {
	if math.IsNaN(float64(gain)) || math.IsInf(float64(gain), 0) || gain < 0 || gain > 1 {
		return "", fmt.Errorf("volume must be 0..100")
	}
	_, count, err := applicationVolume(ctx, application, func(_ float32) (float32, bool) { return gain, true })
	return fmt.Sprintf("%s volume %.0f%% (%d sessions)", application, gain*100, count), err
}

// ApplicationVolumePercent observes supported Core Audio render sessions
func ApplicationVolumePercent(ctx context.Context, application string) (float64, error) {
	value, _, err := applicationVolume(ctx, application, func(current float32) (float32, bool) { return current, false })
	return float64(value) * 100, err
}

// AdjustApplicationVolume applies the requested application-volume adjustment
func AdjustApplicationVolume(ctx context.Context, application string, delta float64) (string, error) {
	if math.IsNaN(delta) || math.IsInf(delta, 0) || math.Abs(delta) > 86400 {
		return "", fmt.Errorf("invalid volume delta")
	}
	value, count, err := applicationVolume(ctx, application, func(current float32) (float32, bool) { return min(1, max(0, current+float32(delta/100))), true })
	return fmt.Sprintf("%s volume %.0f%% (%d sessions)", application, value*100, count), err
}

// applicationVolume reads or updates the selected application audio session
func applicationVolume(ctx context.Context, application string, update func(float32) (float32, bool)) (float32, int, error) {
	if application == "" {
		return 0, 0, fmt.Errorf("application required")
	}
	var observed float32
	count := 0
	err := apartment(func(e *object) error {
		var d *object
		if _, x := call(e, 4, 0, 0, uintptr(unsafe.Pointer(&d))); x != nil {
			return x
		}
		defer release(d)
		iid := guid{0x77aa99a0, 0x1bd6, 0x484f, [8]byte{0x8b, 0xc7, 0x2c, 0x65, 0x4c, 0x9a, 0x9b, 0x6f}}
		var manager *object
		if _, x := call(d, 3, uintptr(unsafe.Pointer(&iid)), 23, 0, uintptr(unsafe.Pointer(&manager))); x != nil {
			return x
		}
		defer release(manager)
		var sessions *object
		if _, x := call(manager, 5, uintptr(unsafe.Pointer(&sessions))); x != nil {
			return x
		}
		defer release(sessions)
		var n int32
		if _, x := call(sessions, 3, uintptr(unsafe.Pointer(&n))); x != nil {
			return x
		}
		controlIID := guid{0xbfb7ff88, 0x7239, 0x4fc9, [8]byte{0x8f, 0xa2, 0x07, 0xc9, 0x50, 0xbe, 0x9c, 0x6d}}
		simpleIID := guid{0x87ce5498, 0x68d6, 0x44e5, [8]byte{0x92, 0x15, 0x6d, 0xa4, 0x7e, 0xf8, 0x83, 0xd8}}
		for i := int32(0); i < n; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			var session *object
			if _, x := call(sessions, 4, uintptr(i), uintptr(unsafe.Pointer(&session))); x != nil {
				return x
			}
			func() {
				defer release(session)
				var control *object
				if _, x := call(session, 0, uintptr(unsafe.Pointer(&controlIID)), uintptr(unsafe.Pointer(&control))); x != nil {
					return
				}
				defer release(control)
				var pid uint32
				if _, x := call(control, 14, uintptr(unsafe.Pointer(&pid))); x != nil {
					return
				}
				h, x := syscall.OpenProcess(0x1000, false, pid)
				if x != nil {
					return
				}
				defer syscall.CloseHandle(h)
				buffer := make([]uint16, 32768)
				size := uint32(len(buffer))
				ok, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW").Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
				if ok == 0 {
					return
				}
				name := strings.ToLower(syscall.UTF16ToString(buffer[:size]))
				if !strings.HasSuffix(name, `\`+strings.ToLower(application)+".exe") {
					return
				}
				var simple *object
				if _, x := call(session, 0, uintptr(unsafe.Pointer(&simpleIID)), uintptr(unsafe.Pointer(&simple))); x != nil {
					return
				}
				defer release(simple)
				var current float32
				if _, x := call(simple, 4, uintptr(unsafe.Pointer(&current))); x != nil {
					return
				}
				gain, write := update(current)
				if write {
					if _, x := call(simple, 3, uintptr(math.Float32bits(gain)), 0); x != nil {
						return
					}
				}
				var actual float32
				if _, x := call(simple, 4, uintptr(unsafe.Pointer(&actual))); x == nil && (!write || math.Abs(float64(actual-gain)) < .001) {
					observed += actual
					count++
				}
			}()
		}
		if count == 0 {
			return fmt.Errorf("no controllable output session for %s", application)
		}
		return nil
	})
	if count > 0 {
		observed /= float32(count)
	}
	return observed, count, err
}

// captureOnly rejects endpoints that are not audio capture devices
func captureOnly(d *object) error {
	iid := guid{0x1be09788, 0x6894, 0x4089, [8]byte{0x85, 0x86, 0x9a, 0x2a, 0x6c, 0x26, 0x5a, 0xc5}}
	var endpoint *object
	if _, e := call(d, 0, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&endpoint))); e != nil {
		return e
	}
	defer release(endpoint)
	var flow int32
	if _, e := call(endpoint, 3, uintptr(unsafe.Pointer(&flow))); e != nil {
		return e
	}
	if flow != 1 {
		return fmt.Errorf("selected endpoint is not a microphone capture input")
	}
	return nil
}
