//go:build windows

package spotify

import (
	"context"
	"encoding/base64"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	configvalue "github.com/StreamDockManager/Plugin-SDK/Go/settings"
	"github.com/StreamDockManager/Plugins/internal/microphone"
)

// Spotify reads or controls Spotify through the native media adapter
func Spotify(ctx context.Context, action string, s map[string]any) (string, error) {
	delta, relative, err := adjustment(s)
	if err != nil {
		return "", err
	}
	// Core Audio owns its own COM apartment; perform volume work before WinRT initialization
	if action == "volume" {
		if relative {
			return microphone.AdjustApplicationVolume(ctx, "spotify", delta)
		}
		return microphone.ApplicationVolume(ctx, "spotify", float32(configvalue.Number(s, "volume", 50)/100))
	}
	var volume *float64
	if action == "status" {
		if value, e := microphone.ApplicationVolumePercent(ctx, "spotify"); e == nil {
			volume = &value
		}
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := roinit.Call(1)
	if int32(hr) < 0 {
		return "", fmt.Errorf("WinRT initialization denied: %x", hr)
	}
	defer rouninit.Call()
	manager, e := spotifyManager(ctx)
	if e != nil {
		return "", e
	}
	defer mrelease(manager)
	session, e := spotifySession(manager)
	if e != nil {
		return "", e
	}
	defer mrelease(session)
	if action == "status" {

		status := SpotifyStatus{Mode: "unknown"}
		rate := 0.0
		var playback *mediaObject
		if mcall(session, 9, uintptr(unsafe.Pointer(&playback))) == nil && playback != nil {
			defer mrelease(playback)
			var mode int32
			if mcall(playback, 7, uintptr(unsafe.Pointer(&mode))) == nil {
				status.Mode = spotifyWindowsMode(mode)
			}
			var reference *mediaObject
			if mcall(playback, 10, uintptr(unsafe.Pointer(&reference))) == nil && reference != nil {
				defer mrelease(reference)
				_ = mcall(reference, 6, uintptr(unsafe.Pointer(&rate)))
			}
		}
		var timeline *mediaObject
		if mcall(session, 8, uintptr(unsafe.Pointer(&timeline))) == nil && timeline != nil {
			defer mrelease(timeline)
			var start, end, position, updated int64
			_ = mcall(timeline, 6, uintptr(unsafe.Pointer(&start)))
			_ = mcall(timeline, 7, uintptr(unsafe.Pointer(&end)))
			_ = mcall(timeline, 11, uintptr(unsafe.Pointer(&updated)))
			if mcall(timeline, 10, uintptr(unsafe.Pointer(&position))) == nil {
				now := time.Now().UnixNano()/100 + 116444736000000000
				status = spotifyWindowsTimeline(status.Mode, start, end, position, updated, now, rate)
			}
		}
		status.VolumePercent = volume
		{
			var op, properties *mediaObject
			if mcall(session, 7, uintptr(unsafe.Pointer(&op))) == nil && await(ctx, op, unsafe.Pointer(&properties)) == nil && properties != nil {
				defer mrelease(properties)
				status.Title, _ = mediaString(properties, 6)
				status.Artist, _ = mediaString(properties, 9)
				if configvalue.Bool(s, "include_artwork") {
					status.Artwork, _ = thumbnail(ctx, properties)
				}
			}
		}
		return spotifyStatusJSON(status)
	}
	index := map[string]int{"toggle": 20, "next": 16, "previous": 17, "seek": 24}[action]
	if index == 0 {
		return "", fmt.Errorf("unsupported Spotify action")
	}
	var operation *mediaObject
	if action == "seek" {
		position := int64(configvalue.Number(s, "position", 0) * 1e7)
		if relative {
			var timeline *mediaObject
			if e = mcall(session, 8, uintptr(unsafe.Pointer(&timeline))); e != nil || timeline == nil {
				return "", fmt.Errorf("Spotify timeline is unavailable")
			}
			defer mrelease(timeline)
			var start, end, current int64
			if e = mcall(timeline, 10, uintptr(unsafe.Pointer(&current))); e != nil {
				return "", e
			}
			_ = mcall(timeline, 6, uintptr(unsafe.Pointer(&start)))
			_ = mcall(timeline, 7, uintptr(unsafe.Pointer(&end)))
			position = max(start, current+int64(delta*1e7))
			if end > start {
				position = min(end, position)
			}
		}
		e = mcall(session, index, uintptr(position), uintptr(unsafe.Pointer(&operation)))
	} else {
		e = mcall(session, index, uintptr(unsafe.Pointer(&operation)))
	}
	if e != nil {
		return "", e
	}
	var success uint8
	if e = await(ctx, operation, unsafe.Pointer(&success)); e != nil {
		return "", e
	}
	if success == 0 {
		return "", fmt.Errorf("Spotify rejected the requested control")
	}
	return "Playback updated", nil
}

// await waits for the Windows asynchronous operation to finish
func await(ctx context.Context, async *mediaObject, result unsafe.Pointer) error {
	return awaitAt(ctx, async, result, 8)
}

// awaitAt waits through the selected asynchronous interface methods
func awaitAt(ctx context.Context, async *mediaObject, result unsafe.Pointer, index int) error {
	defer mrelease(async)
	iid := mediaGUID{0x00000036, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	var info *mediaObject
	if e := mcall(async, 0, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&info))); e != nil {
		return e
	}
	defer mrelease(info)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status int32
		if e := mcall(info, 7, uintptr(unsafe.Pointer(&status))); e != nil {
			return e
		}
		if status == 1 {
			break
		}
		if status != 0 {
			return fmt.Errorf("Windows media operation failed or cancelled (%d)", status)
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			_ = mcall(info, 9)
			return fmt.Errorf("Windows media operation timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return mcall(async, index, uintptr(result))
}

// hstring creates a Windows runtime string
func hstring(s string) (uintptr, error) {
	u, e := syscall.UTF16FromString(s)
	if e != nil {
		return 0, e
	}
	var h uintptr
	hr, _, _ := hcreate.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h)))
	if int32(hr) < 0 {
		return 0, fmt.Errorf("Windows string error %x", hr)
	}
	return h, nil
}

// hstringValue reads the text from a Windows runtime string
func hstringValue(h uintptr) string {
	var n uint32
	p, _, _ := hbuffer.Call(h, uintptr(unsafe.Pointer(&n)))
	if p == 0 || n == 0 || n > 8192 {
		return ""
	}

	units := make([]uint16, int(n))
	moveMemory.Call(uintptr(unsafe.Pointer(&units[0])), p, uintptr(n)*unsafe.Sizeof(units[0]))
	return syscall.UTF16ToString(units)
}

// mcall invokes a Windows media interface method
func mcall(o *mediaObject, index int, args ...uintptr) error {
	if o == nil {
		return fmt.Errorf("Spotify session unavailable")
	}
	hr, _, _ := syscall.SyscallN(o.v[index], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	if int32(hr) < 0 {
		return fmt.Errorf("Windows media control denied: HRESULT 0x%x", hr)
	}
	return nil
}

// mediaString reads a Windows media string property
func mediaString(o *mediaObject, index int) (string, error) {
	var h uintptr
	if e := mcall(o, index, uintptr(unsafe.Pointer(&h))); e != nil {
		return "", e
	}
	defer hdelete.Call(h)
	return hstringValue(h), nil
}

// mrelease releases a Windows media interface reference
func mrelease(o *mediaObject) {
	if o != nil {
		_ = mcall(o, 2)
	}
}

// spotifyManager opens the Windows media session manager
func spotifyManager(ctx context.Context) (*mediaObject, error) {
	class, e := hstring("Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager")
	if e != nil {
		return nil, e
	}
	defer hdelete.Call(class)
	iid := mediaGUID{0x2050c4ee, 0x11a0, 0x57de, [8]byte{0xae, 0xd7, 0xc9, 0x7c, 0x70, 0x33, 0x82, 0x45}}
	var factory *mediaObject
	hr, _, _ := rofactory.Call(class, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&factory)))
	if int32(hr) < 0 {
		return nil, fmt.Errorf("Windows media session API unavailable: %x", hr)
	}
	defer mrelease(factory)
	var async, manager *mediaObject
	if e = mcall(factory, 6, uintptr(unsafe.Pointer(&async))); e != nil {
		return nil, e
	}
	if e = await(ctx, async, unsafe.Pointer(&manager)); e != nil {
		return nil, e
	}
	return manager, nil
}

// spotifySession selects the Spotify media session
func spotifySession(manager *mediaObject) (*mediaObject, error) {
	var sessions *mediaObject
	if e := mcall(manager, 7, uintptr(unsafe.Pointer(&sessions))); e != nil {
		return nil, e
	}
	defer mrelease(sessions)
	var size uint32
	if e := mcall(sessions, 7, uintptr(unsafe.Pointer(&size))); e != nil {
		return nil, e
	}
	if size > 1024 {
		return nil, fmt.Errorf("Media session inventory exceeds limit")
	}
	var session *mediaObject
	for i := uint32(0); i < size; i++ {
		var candidate *mediaObject
		if e := mcall(sessions, 6, uintptr(i), uintptr(unsafe.Pointer(&candidate))); e != nil {
			return nil, e
		}
		name, err := mediaString(candidate, 6)
		if err == nil && strings.Contains(strings.ToLower(name), "spotify") {
			session = candidate
			break
		}
		mrelease(candidate)
	}
	if session == nil {
		return nil, fmt.Errorf("Spotify is not exposing a local media session")
	}
	return session, nil
}

// thumbnail reads and bounds the current media artwork
func thumbnail(ctx context.Context, properties *mediaObject) (string, error) {
	var reference *mediaObject
	if e := mcall(properties, 15, uintptr(unsafe.Pointer(&reference))); e != nil || reference == nil {
		return "", e
	}
	defer mrelease(reference)
	var op, stream *mediaObject
	if e := mcall(reference, 6, uintptr(unsafe.Pointer(&op))); e != nil {
		return "", e
	}
	if e := await(ctx, op, unsafe.Pointer(&stream)); e != nil {
		return "", e
	}
	defer mrelease(stream)
	iid := mediaGUID{0x905a0fe1, 0xbc53, 0x11df, [8]byte{0x8c, 0x49, 0x00, 0x1e, 0x4f, 0xc6, 0x86, 0xda}}
	var random *mediaObject
	if e := mcall(stream, 0, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&random))); e != nil {
		return "", e
	}
	defer mrelease(random)
	var size uint64
	if e := mcall(random, 6, uintptr(unsafe.Pointer(&size))); e != nil {
		return "", e
	}
	if size == 0 || size > 10<<20 {
		return "", fmt.Errorf("artwork exceeds limit")
	}
	var input *mediaObject
	if e := mcall(random, 8, 0, uintptr(unsafe.Pointer(&input))); e != nil {
		return "", e
	}
	defer mrelease(input)
	name, e := hstring("Windows.Storage.Streams.Buffer")
	if e != nil {
		return "", e
	}
	defer hdelete.Call(name)
	factoryIID := mediaGUID{0x71af914d, 0xc10f, 0x484b, [8]byte{0xbc, 0x50, 0x14, 0xbc, 0x62, 0x3b, 0x3a, 0x27}}
	var factory *mediaObject
	hr, _, _ := rofactory.Call(name, uintptr(unsafe.Pointer(&factoryIID)), uintptr(unsafe.Pointer(&factory)))
	if int32(hr) < 0 {
		return "", fmt.Errorf("image buffer factory unavailable")
	}
	defer mrelease(factory)
	var buffer *mediaObject
	if e = mcall(factory, 6, uintptr(size), uintptr(unsafe.Pointer(&buffer))); e != nil {
		return "", e
	}
	defer mrelease(buffer)
	var readOp, result *mediaObject
	if e = mcall(input, 6, uintptr(unsafe.Pointer(buffer)), uintptr(size), 0, uintptr(unsafe.Pointer(&readOp))); e != nil {
		return "", e
	}
	if e = awaitAt(ctx, readOp, unsafe.Pointer(&result), 10); e != nil {
		return "", e
	}
	defer mrelease(result)
	var length uint32
	if e = mcall(result, 7, uintptr(unsafe.Pointer(&length))); e != nil {
		return "", e
	}
	if uint64(length) > size {
		return "", fmt.Errorf("invalid artwork length")
	}
	byteIID := mediaGUID{0x905a0fef, 0xbc53, 0x11df, [8]byte{0x8c, 0x49, 0x00, 0x1e, 0x4f, 0xc6, 0x86, 0xda}}
	var access *mediaObject
	if e = mcall(result, 0, uintptr(unsafe.Pointer(&byteIID)), uintptr(unsafe.Pointer(&access))); e != nil {
		return "", e
	}
	defer mrelease(access)
	var pointer *byte
	if e = mcall(access, 3, uintptr(unsafe.Pointer(&pointer))); e != nil {
		return "", e
	}
	if pointer == nil {
		return "", fmt.Errorf("no artwork bytes")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(unsafe.Slice(pointer, int(length))), nil
}

type mediaGUID struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

type mediaObject struct{ v *[40]uintptr }

var combase = syscall.NewLazyDLL("combase.dll")

var hbuffer = combase.NewProc("WindowsGetStringRawBuffer")

var hcreate = combase.NewProc("WindowsCreateString")

var hdelete = combase.NewProc("WindowsDeleteString")

var moveMemory = syscall.NewLazyDLL("kernel32.dll").NewProc("RtlMoveMemory")

var rofactory = combase.NewProc("RoGetActivationFactory")

var roinit = combase.NewProc("RoInitialize")

var rouninit = combase.NewProc("RoUninitialize")
