package mqtt

import (
	c "github.com/StreamDockManager/Plugins/internal/connectors"
	"testing"
)

func TestDemandSeparatesVaultAndOrdinarySubscriptions(t *testing.T) {
	d := NewDriver(make(chan struct{}, 1))
	configured := map[string]any{"_action": "text", "topic": "ordinary"}
	password := map[string]any{"_action": "text", "topic": "private", "use_vault_password": true}
	both := map[string]any{"_action": "text", "topic": "separate", "use_vault_password": true, "use_vault_username": true}
	d.Demand([]map[string]any{configured, password, both})
	for _, s := range []map[string]any{configured, password, both} {
		topics := d.demandByScope[c.VaultKey(s, Definition.Slug)]
		if _, ok := topics[s["topic"].(string)]; !ok || len(topics) != 1 {
			t.Fatal("subscription crossed a credential scope")
		}
	}
}
