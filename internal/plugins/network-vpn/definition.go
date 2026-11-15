package networkvpn

import "github.com/StreamDockManager/Plugins/internal/engine"

var Definition = engine.Definition{ViewTargets: viewTargets, RuntimeAssets: []string{"icons/globe", "icons/network", "icons/shield"}, Slug: "network-vpn", Name: "Network & VPN Status", Version: "0.1.0", MinimumManager: "0.1.0", RequiredCapabilities: []string{"plugin-protocol-v1", "plugin-theme-v1", "plugin-settings-v1", "manager-controls-v1"}, Actions: []string{"interfaces", "reachability", "vpn"}}

// Each target lists the actions allowed to update its physical views
var viewTargets = []engine.ViewTarget{
	{Action: "interfaces", UpdatedBy: []string{"interfaces"}, Live: true},
	{Action: "reachability", UpdatedBy: []string{"reachability"}, Live: true},
	{Action: "vpn", UpdatedBy: []string{"vpn"}, Live: true},
}

var visuals = map[string]engine.Visual{"interfaces": {Icon: "icons/network", States: []string{"icons/network"}}, "reachability": {Icon: "icons/globe", States: []string{"icons/globe"}}, "vpn": {Icon: "icons/shield", States: []string{"icons/shield"}}}
