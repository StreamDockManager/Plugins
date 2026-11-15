//go:build !linux && !windows

package spotify

// ObserveSpotify starts platform media change observation when available
func ObserveSpotify(chan<- struct{}) MediaObserver { return nil }
