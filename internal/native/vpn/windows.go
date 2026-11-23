//go:build windows

package vpn

import (
	"context"
	"errors"
	"fmt"
	"github.com/StreamDockManager/Plugins/internal/native/observation"
	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"

	process "github.com/StreamDockManager/Plugins/internal/native/command"
)

var query = observation.Query{Slot: make(chan struct{}, 1)}
var arrayElement = windows.NewLazySystemDLL("oleaut32.dll").NewProc("SafeArrayGetElement")

type vpnRow struct{ Name, ConnectionStatus string }

// Query the same current-user CIM provider as Get-VpnConnection. Enumerating
// active RAS connections alone omits the user's disconnected profiles.
func nativeVPN() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var status *ole.OleError
		if !errors.As(err, &status) || status.Code() != 1 {
			return "", err
		}
	}
	defer ole.CoUninitialize()
	service, err := ole.GetObject(`winmgmts:\\.\root\Microsoft\Windows\RemoteAccess\Client`, nil, ole.IID_IDispatch)
	if err != nil {
		return "", err
	}
	defer service.Release()
	services, err := service.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return "", err
	}
	defer services.Release()
	out, err := currentUserProfiles(services)
	if err != nil {
		return "", err
	}
	defer out.Clear()
	result := out.ToIDispatch()
	if result == nil {
		return "", fmt.Errorf("missing VPN provider response")
	}
	status, err := oleutil.GetProperty(result, "ReturnValue")
	if err != nil {
		return "", err
	}
	defer status.Clear()
	if fmt.Sprint(status.Value()) != "0" {
		return "", fmt.Errorf("VPN provider returned %v", status.Value())
	}
	value, err := oleutil.GetProperty(result, "cmdletOutput")
	if err != nil {
		return "", err
	}
	defer value.Clear()
	if value.VT == ole.VT_NULL || value.VT == ole.VT_EMPTY {
		return "", nil
	}
	array := value.ToArray()
	if array == nil {
		return "", fmt.Errorf("invalid VPN provider array")
	}
	kind, err := array.GetType()
	if err != nil || (ole.VT(kind) != ole.VT_UNKNOWN && ole.VT(kind) != ole.VT_DISPATCH) {
		return "", fmt.Errorf("unsupported VPN provider row type")
	}
	n, err := array.TotalElements(1)
	if err != nil || n < 0 || n > 10000 {
		return "", fmt.Errorf("invalid VPN provider row count")
	}
	rows := make([]vpnRow, 0, n)
	for i := int32(0); i < n; i++ {
		var unknown *ole.IUnknown
		code, _, _ := arrayElement.Call(uintptr(unsafe.Pointer(array.Array)), uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&unknown)))
		if code != 0 || unknown == nil {
			return "", fmt.Errorf("invalid VPN provider row")
		}
		row, err := readRow(unknown)
		unknown.Release()
		if err != nil {
			return "", err
		}
		rows = append(rows, row)
	}
	return observation.Rows(rows, "")
}

// A provider with an incompatible automation schema falls back to the command adapter.
func dispatchResult(value *ole.VARIANT, err error) (*ole.VARIANT, error) {
	if err != nil {
		return nil, err
	}
	if value != nil && value.ToIDispatch() != nil {
		return value, nil
	}
	if value != nil {
		_ = value.Clear()
	}
	return nil, fmt.Errorf("VPN provider did not return a dispatch object")
}

// Create the Get method input with the same current-user scope as Get-VpnConnection.
func currentUserProfiles(services *ole.IDispatch) (*ole.VARIANT, error) {
	class, err := dispatchResult(oleutil.CallMethod(services, "Get", "PS_VpnConnection"))
	if err != nil {
		return nil, err
	}
	defer class.Clear()
	methods, err := dispatchResult(oleutil.GetProperty(class.ToIDispatch(), "Methods_"))
	if err != nil {
		return nil, err
	}
	defer methods.Clear()
	method, err := dispatchResult(oleutil.CallMethod(methods.ToIDispatch(), "Item", "Get"))
	if err != nil {
		return nil, err
	}
	defer method.Clear()
	inputClass, err := dispatchResult(oleutil.GetProperty(method.ToIDispatch(), "InParameters"))
	if err != nil {
		return nil, err
	}
	defer inputClass.Clear()
	input, err := dispatchResult(oleutil.CallMethod(inputClass.ToIDispatch(), "SpawnInstance_"))
	if err != nil {
		return nil, err
	}
	defer input.Clear()
	scope, err := oleutil.PutProperty(input.ToIDispatch(), "AllUserConnection", false)
	if err != nil {
		return nil, err
	}
	defer scope.Clear()
	return oleutil.CallMethod(services, "ExecMethod", "PS_VpnConnection", "Get", input.ToIDispatch())
}

func readRow(object *ole.IUnknown) (vpnRow, error) {
	var row vpnRow
	dispatch, err := object.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return row, err
	}
	defer dispatch.Release()
	for _, field := range []struct {
		name   string
		target *string
	}{{"Name", &row.Name}, {"ConnectionStatus", &row.ConnectionStatus}} {
		value, err := oleutil.GetProperty(dispatch, field.name)
		if err != nil {
			return row, err
		}
		text, ok := value.Value().(string)
		value.Clear()
		if !ok {
			return row, fmt.Errorf("invalid VPN %s", field.name)
		}
		*field.target = text
	}
	return row, nil
}

// VPN reads the platform VPN connection status
func VPN(ctx context.Context) (string, error) {
	text, err := query.Read(ctx, nativeVPN)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return text, err
	}
	return ps(ctx, `Get-VpnConnection | Select-Object Name,ConnectionStatus | ConvertTo-Json -Compress`)
}

// ps runs the platform helper through the bounded PowerShell adapter
func ps(ctx context.Context, s string) (string, error) {
	return process.Command(ctx, "powershell.exe", powershellArgs(s)...)
}
