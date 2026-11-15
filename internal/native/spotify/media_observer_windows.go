//go:build windows

package spotify

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// ObserveSpotify starts platform media change observation when available
func ObserveSpotify(wake chan<- struct{}) MediaObserver {
	return newMediaObservation(wake, func(ctx context.Context, o *mediaObservation) {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hr, _, _ := roinit.Call(1)
		if int32(hr) < 0 {
			return
		}
		defer rouninit.Call()
		manager, err := spotifyManager(ctx)
		if err != nil {
			return
		}
		defer mrelease(manager)
		rebind := make(chan struct{}, 1)
		changed := mediaGUID{0x2e2a8630, 0xdc8c, 0x530a, [8]byte{0x97, 0x46, 0xbc, 0x98, 0x4d, 0x4b, 0x02, 0x9e}}
		listener, err := listenMedia(manager, 10, newMediaDelegate(o, changed, rebind))
		if err != nil {
			return
		}
		defer listener.close()
		var session *mediaObject
		var listeners []mediaListener
		clear := func() {
			for _, l := range listeners {
				l.close()
			}
			listeners = nil
			mrelease(session)
			session = nil
		}
		defer clear()
		bind := func() bool {
			clear()
			session, err = spotifySession(manager)
			if err != nil {
				return false
			}
			for _, event := range []struct {
				slot int
				iid  mediaGUID
			}{
				{25, mediaGUID{0xe8bf62af, 0xfac1, 0x5fff, [8]byte{0x90, 0x53, 0x0b, 0xf1, 0x91, 0xae, 0x77, 0x7e}}},
				{27, mediaGUID{0x2bdf1426, 0xd41f, 0x5896, [8]byte{0x89, 0x7f, 0xef, 0xc0, 0xb0, 0xfa, 0x73, 0x92}}},
				{29, mediaGUID{0x0f2ce2b7, 0xafa7, 0x5ed0, [8]byte{0x8c, 0xb6, 0x8c, 0x40, 0xcf, 0x9b, 0x3a, 0x5f}}},
			} {
				l, err := listenMedia(session, event.slot, newMediaDelegate(o, event.iid, nil))
				if err != nil {
					clear()
					return false
				}
				listeners = append(listeners, l)
			}
			return true
		}
		o.ready(bind())

		timer := time.NewTicker(30 * time.Second)
		defer timer.Stop()
		lastBind := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-rebind:
				if remaining := 250*time.Millisecond - time.Since(lastBind); remaining > 0 {
					delay := time.NewTimer(remaining)
					select {
					case <-ctx.Done():
						delay.Stop()
						return
					case <-delay.C:
					}
				}
				o.ready(bind())
				lastBind = time.Now()
			case <-timer.C:
				o.ready(bind())
				lastBind = time.Now()
			}
		}
	})
}

// listenMedia subscribes to native Windows media notifications
func listenMedia(object *mediaObject, add int, delegate *mediaDelegate) (mediaListener, error) {
	var token int64
	if err := mcall(object, add, uintptr(unsafe.Pointer(delegate)), uintptr(unsafe.Pointer(&token))); err != nil {
		releaseMediaDelegate(uintptr(unsafe.Pointer(delegate)))
		return mediaListener{}, err
	}
	return mediaListener{object, add + 1, token, delegate}, nil
}

// close releases the native media subscription
func (l mediaListener) close() {
	if l.object != nil {
		_ = mcall(l.object, l.remove, uintptr(l.token))
		releaseMediaDelegate(uintptr(unsafe.Pointer(l.delegate)))
	}
}

// newMediaDelegate allocates the callback delegate for native media events
func newMediaDelegate(o *mediaObservation, iid mediaGUID, rebind chan<- struct{}) *mediaDelegate {
	prepareMediaDelegateTable()
	d := &mediaDelegate{v: &mediaDelegateTable, iid: iid, observation: o, rebind: rebind}
	d.refs.Store(1)
	d.pin.Pin(d)
	mediaDelegates.Store(uintptr(unsafe.Pointer(d)), d)
	return d
}

// prepareMediaDelegateTable initializes the Windows delegate method table
func prepareMediaDelegateTable() {
	mediaDelegateOnce.Do(func() {
		mediaDelegateTable = [4]uintptr{
			syscall.NewCallback(func(self, requested, result uintptr) uintptr {
				if requested == 0 || result == 0 {
					return 0x80004003
				}
				var out uintptr
				moveMemory.Call(result, uintptr(unsafe.Pointer(&out)), unsafe.Sizeof(out))
				value, ok := mediaDelegates.Load(self)
				if !ok {
					return 0x80004002
				}
				d := value.(*mediaDelegate)
				var iid mediaGUID
				moveMemory.Call(uintptr(unsafe.Pointer(&iid)), requested, unsafe.Sizeof(iid))
				unknown := mediaGUID{0, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
				agile := mediaGUID{0x94ea2b94, 0xe9cc, 0x49e0, [8]byte{0xc0, 0xff, 0xee, 0x64, 0xca, 0x8f, 0x5b, 0x90}}
				if iid != d.iid && iid != unknown && iid != agile {
					return 0x80004002
				}
				d.refs.Add(1)
				out = self
				moveMemory.Call(result, uintptr(unsafe.Pointer(&out)), unsafe.Sizeof(out))
				return 0
			}),
			syscall.NewCallback(func(self uintptr) uintptr {
				if value, ok := mediaDelegates.Load(self); ok {
					return uintptr(value.(*mediaDelegate).refs.Add(1))
				}
				return 0
			}),
			syscall.NewCallback(func(self uintptr) uintptr { return releaseMediaDelegate(self) }),
			syscall.NewCallback(func(self, _, _ uintptr) uintptr {
				if value, ok := mediaDelegates.Load(self); ok {
					d := value.(*mediaDelegate)
					d.observation.invalidate()
					if d.rebind != nil {
						select {
						case d.rebind <- struct{}{}:
						default:
						}
					}
				}
				return 0
			}),
		}

	})
}

// releaseMediaDelegate releases the retained native delegate
func releaseMediaDelegate(address uintptr) uintptr {
	if value, ok := mediaDelegates.Load(address); ok {
		d := value.(*mediaDelegate)
		left := d.refs.Add(-1)
		if left == 0 {
			mediaDelegates.Delete(address)
			d.pin.Unpin()
		}
		return uintptr(left)
	}
	return 0
}

type mediaDelegate struct {
	v           *[4]uintptr
	refs        atomic.Int32
	iid         mediaGUID
	observation *mediaObservation
	rebind      chan<- struct{}
	pin         runtime.Pinner
}

type mediaListener struct {
	object   *mediaObject
	remove   int
	token    int64
	delegate *mediaDelegate
}

var mediaDelegateOnce sync.Once

var mediaDelegateTable [4]uintptr

var mediaDelegates sync.Map
