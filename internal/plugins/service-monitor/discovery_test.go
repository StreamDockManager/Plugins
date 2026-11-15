package servicemonitor

import (
	"reflect"
	"testing"
)

// Verify service discovery uses names accepted by the status check
func TestServiceDiscoveryUsesNamesAcceptedByTheStatusCheck(t *testing.T) {
	for _, tt := range []struct {
		platform string
		output   string
		names    []string
	}{
		{"darwin", "PID\tStatus\tLabel\n-\t0\tcom.example.idle\n12\t0\tcom.example.active\n", []string{"com.example.active", "com.example.idle"}},
		{"linux", "z.service disabled enabled\na.service enabled enabled\na.service enabled enabled\nnoise.timer enabled\n", []string{"a.service", "z.service"}},
		{"windows", "WSearch\r\nSpooler\r\n\r\n", []string{"Spooler", "WSearch"}},
	} {
		t.Run(tt.platform, func(t *testing.T) {
			var names []string
			for _, choice := range serviceChoices(tt.platform, tt.output) {
				if choice.ID != choice.Name || choice.Kind != "service" || !serviceName.MatchString(choice.ID) {
					t.Fatalf("unexpected service choice: %+v", choice)
				}
				names = append(names, choice.ID)
			}
			if !reflect.DeepEqual(names, tt.names) {
				t.Fatalf("got %v, want %v", names, tt.names)
			}
		})
	}
}
