package weather

import (
	"github.com/StreamDockManager/Plugin-SDK/Go/settings"
	metadata "github.com/StreamDockManager/Plugins/internal/settings"
)

// Declare the weather global settings fields
func PluginFields() []settings.Field {
	return []settings.Field{metadata.Text("geocoding_endpoint", "Nominatim search endpoint (HTTPS)", "https://nominatim.openstreetmap.org/search"), metadata.Check("geocoding_postal_only", "Postal-only service (shared public Nominatim broker)")}
}

// Declare settings supported by the selected weather action
func ActionFields(a string) []settings.Field {
	return []settings.Field{metadata.Numeric("latitude", "Latitude", 0, -90, 90), metadata.Numeric("longitude", "Longitude", 0, -180, 180), metadata.Select("units", "Temperature units", "celsius", "celsius", "fahrenheit"), metadata.Text("endpoint", "HTTPS service endpoint", "https://api.open-meteo.com/v1/forecast"), metadata.Check("noncommercial", "Confirm noncommercial Open-Meteo use")}
}
