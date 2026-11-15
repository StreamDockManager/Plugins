//go:build linux

package spotify

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"syscall"
)

// ObserveSpotify starts platform media change observation when available
func ObserveSpotify(wake chan<- struct{}) MediaObserver {
	return newMediaObservation(wake, func(ctx context.Context, o *mediaObservation) {

		command := exec.Command("dbus-monitor", "--session", "--profile",
			"type='signal',sender='org.mpris.MediaPlayer2.spotify',path='/org/mpris/MediaPlayer2',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged'",
			"type='signal',sender='org.mpris.MediaPlayer2.spotify',path='/org/mpris/MediaPlayer2',interface='org.mpris.MediaPlayer2.Player',member='Seeked'",
			"type='signal',sender='org.freedesktop.DBus',interface='org.freedesktop.DBus',member='NameOwnerChanged',arg0='org.mpris.MediaPlayer2.spotify'")
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Stderr = io.Discard
		pipe, err := command.StdoutPipe()
		if err != nil {
			return
		}
		if err = command.Start(); err != nil {
			_ = pipe.Close()
			return
		}
		pid := command.Process.Pid
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			select {
			case <-ctx.Done():
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			case <-stop:
			}
		}()
		o.ready(true)
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 1024), 4096)
		for scanner.Scan() {
			if ctx.Err() != nil {
				break
			}
			line := strings.TrimSpace(scanner.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				o.invalidate()
			}
		}

		if scanner.Err() != nil {
			// A truncated or failed monitor stream must not remain healthy during teardown
			o.ready(false)
		}

		_ = syscall.Kill(-pid, syscall.SIGKILL)
		close(stop)
		<-stopped
		_ = command.Wait()
	})
}
