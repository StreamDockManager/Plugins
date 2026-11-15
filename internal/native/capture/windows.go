//go:build windows

package capture

import (
	"context"
	"fmt"
	"strings"
)

// Capture captures the selected display through the platform backend
func Capture(ctx context.Context, mode, path string) error {
	if mode != "display" && mode != "window" && mode != "region" && mode != "clipboard" {
		return fmt.Errorf("unknown capture mode")
	}
	script := `Add-Type -AssemblyName System.Windows.Forms,System.Drawing; Add-Type 'using System;using System.Runtime.InteropServices; public class SDCapture{[StructLayout(LayoutKind.Sequential)] public struct R{public int L,T,Rt,B;} [DllImport("user32.dll")]public static extern IntPtr GetForegroundWindow();[DllImport("user32.dll")]public static extern bool GetWindowRect(IntPtr h,out R r);}';$r=[Windows.Forms.SystemInformation]::VirtualScreen;`
	if mode == "window" {
		script += `$w=New-Object SDCapture+R;if(![SDCapture]::GetWindowRect([SDCapture]::GetForegroundWindow(),[ref]$w)){throw 'window.Window capture unavailable'};$r=New-Object Drawing.Rectangle $w.L,$w.T,($w.Rt-$w.L),($w.B-$w.T);`
	}
	if mode == "region" {
		script += `$f=New-Object Windows.Forms.Form;$f.FormBorderStyle='None';$f.Bounds=$r;$f.TopMost=$true;$f.Opacity=0.3;$f.BackColor='Black';$f.Cursor=[Windows.Forms.Cursors]::Cross;$script:start=$null;$script:region=$null;$f.Add_MouseDown({$script:start=[Windows.Forms.Cursor]::Position});$f.Add_MouseUp({$end=[Windows.Forms.Cursor]::Position;if($script:start){$x=[Math]::Min($end.X,$script:start.X);$y=[Math]::Min($end.Y,$script:start.Y);$w=[Math]::Abs($end.X-$script:start.X);$h=[Math]::Abs($end.Y-$script:start.Y);$script:region=New-Object Drawing.Rectangle $x,$y,$w,$h};$f.Close()});$f.Add_KeyDown({if($_.KeyCode -eq 'Escape'){$f.Close()}});[void]$f.ShowDialog();$f.Dispose();if(!$script:region -or $script:region.Width -lt 1 -or $script:region.Height -lt 1){throw 'Capture cancelled'};$r=$script:region;Start-Sleep -Milliseconds 100;`
	}
	script += `$b=New-Object Drawing.Bitmap $r.Width,$r.Height;$g=[Drawing.Graphics]::FromImage($b);try{$g.CopyFromScreen($r.Left,$r.Top,0,0,$b.Size);$b.Save(` + quote(path) + `,[Drawing.Imaging.ImageFormat]::Png);`
	if mode == "clipboard" {
		script += `[Windows.Forms.Clipboard]::SetImage($b);`
	}
	script += `}finally{$g.Dispose();$b.Dispose()}`
	_, e := ps(ctx, mode, script)
	return e
}

// ps runs the platform helper through the bounded PowerShell adapter
func ps(ctx context.Context, mode, script string) (string, error) {
	return captureCommand(ctx, mode, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", `$ErrorActionPreference="Stop";`+script)
}

// quote escapes a value for the PowerShell command
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
