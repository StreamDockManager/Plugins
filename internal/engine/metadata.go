package engine

// CloneVisual keeps catalog consumers from mutating runtime state ordering
func CloneVisual(value Visual) Visual {
	value.States = append([]string(nil), value.States...)
	return value
}

// AdditiveDial is action semantics, not the supported hardware control inventory
func AdditiveDial(slug, action string) bool {
	action = Action(action)
	return (slug == "obs-studio" && action == "volume") ||
		(slug == "home-assistant" && action == "brightness") || slug == "counter-tally" ||
		((slug == "spotify" || slug == "vlc") && (action == "volume" || action == "seek"))
}

// AdditiveActions collects actions whose dial input can be combined
func AdditiveActions(def Definition) []string {
	var actions []string
	for _, action := range def.Actions {
		if AdditiveDial(def.Slug, action) {
			actions = append(actions, action)
		}
	}
	return actions
}
