package randomizerdice

// PackageGlyph pairs a pip die with a numbered octahedron for the catalog.
func PackageGlyph() string {
	return `<g transform="translate(10 -2) scale(.62)">` + modelFor(8).render(rollPose{angle: 29}) +
		`</g><g transform="translate(-2 4.6) scale(.86)">` + modelFor(6).render(rollPose{}) + "</g>"
}

// The action picker and the idle device views share silhouettes and material
// with the rolling dice. All geometry here is local to Randomizer & Dice.
func ActionGlyphs() map[string]string {
	const enamel = `<g fill="#c61e32" data-sdm-fill="accent" stroke="#f5f7fa" data-sdm-stroke="ink" stroke-width=".65">`
	const silver = `<g fill="#f5f7fa" data-sdm-fill="ink" stroke="none">`
	return map[string]string{
		"icons/heads-or-tails":     coinGlyph(1, rollFrameCount, 0),
		"icons/roll-all":           PackageGlyph(),
		"icons/dice-roll-counter":  modelFor(6).render(rollPose{}) + `<circle cx="18" cy="18" r="5" fill="#0f141d" stroke="#f5f7fa" stroke-width=".5"/><path d="M18 15v6m-3-3h6" stroke="#f5f7fa" stroke-width="1.2"/>`,
		"icons/dice-total-counter": modelFor(6).render(rollPose{}) + `<circle cx="18" cy="18" r="5" fill="#0f141d" stroke="#f5f7fa" stroke-width=".5"/><path d="M20.5 15.5h-5l2.5 2.5-2.5 2.5h5" fill="none" stroke="#f5f7fa" stroke-width=".9" stroke-linecap="round" stroke-linejoin="round"/>`,
		"icons/magic-8-ball":       ballGlyph(rollFrameCount, ""),
		"icons/dice":               modelFor(6).render(rollPose{}),
		"icons/range": enamel +
			`<path d="M2 5.5 4 4h16l2 1.5-2 1.5H4zM2 12l2-1.5h16l2 1.5-2 1.5H4zM2 18.5 4 17h16l2 1.5-2 1.5H4z"/>` +
			`</g>` + silver + `<path d="m7 2.3 2.3 1.6v3.3L7 8.8 4.7 7.2V3.9zM16 8.7l2.3 1.7v3.2L16 15.3l-2.3-1.7v-3.2zM10 15.2l2.3 1.7v3.2L10 21.8l-2.3-1.7v-3.2z"/></g>`,
		"icons/list": enamel +
			`<path d="m1.5 5 2.8-2.5L7 5 4.3 7.5zM1.5 12l2.8-2.5L7 12l-2.7 2.5zM1.5 19l2.8-2.5L7 19l-2.7 2.5z"/>` +
			`</g>` + silver + `<path d="M9 4h14l-1.6 2H9zM9 11h11l-1.6 2H9zM9 18h13l-1.6 2H9z"/></g>`,
		"icons/cards": enamel +
			`<path fill-opacity=".5" d="m1.5 7.5 9-3.4 5.2 15.2-9 3.3z"/><path d="m5.4 4.2 10.1-1.9 3.1 16.3-10.2 1.9z"/><path d="m11.4 1.6 10.5 1.3-2 17.2-10.5-1.3z"/>` +
			`</g>` + silver + `<path d="m16.4 5.5 3 5.1-4.1 4.3-3-5.1z"/><path opacity=".45" d="m11.4 1.6 10.5 1.3-1.3 1.9-8-1z"/></g>`,
		"icons/reset": enamel +
			`<path d="m2.4 2.7 3.2 3.2C8 2.8 12.1 1.7 15.9 3.1c5.7 2.1 8.1 8.3 5.6 13.4-2.5 5.2-8.6 7.3-13.6 4.7-2.5-1.2-4.2-3.1-5.1-5.4l3.4-1.3c1.2 3.1 5 5 8.3 3.9 3.6-1.1 5.4-5 4.2-8.2-1.5-4-6.2-5.8-9.8-3.4l-.9.8 3 3.1H2.4z"/>` +
			`</g>` + silver + `<path opacity=".65" d="m2.4 2.7 3.2 3.2 2.4 1.7 3 3.1-6.4-2.3z"/></g>`,
	}
}
