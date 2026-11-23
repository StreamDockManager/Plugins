package weather

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the weather global settings fields
func PluginFields() []settings.Field {
	return nil
}

// Declare settings supported by the selected weather action
func ActionFields(a string) []settings.Field {
	noncommercial := metadata.Check("noncommercial", "Confirm noncommercial Open-Meteo use")
	noncommercial.Default = true
	return []settings.Field{metadata.Numeric("latitude", "Latitude", 0, -90, 90), metadata.Numeric("longitude", "Longitude", 0, -180, 180), metadata.Select("units", "Temperature units", "celsius", "celsius", "fahrenheit"), metadata.Text("endpoint", "HTTPS service endpoint", "https://api.open-meteo.com/v1/forecast"), noncommercial}
}
