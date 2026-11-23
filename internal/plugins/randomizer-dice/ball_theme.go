package randomizerdice

import (
	sdk "github.com/StreamDockManager/Plugin-SDK/Go"
	"math"
	"strconv"
	"strings"
)

// Inline images are not rewritten by the host's packaged-asset palette mapper.
// Resolve each negotiated segment independently, retaining the legacy artwork
// byte-for-byte when no usable palette segment is supplied.
func tintBallSVG(svg string, colors sdk.ThemeColors) string {
	color := func(value, fallback string) string {
		if landingColor.MatchString(value) {
			return value
		}
		return fallback
	}
	surface := color(colors.Surface, "#10131c")
	border := color(colors.Border, "#687289")
	accent := color(colors.Accent, "#174fb1")
	disc := color(colors.Ink, "#f5f7fa")
	number := "#151821"
	answer := "#fff"
	if landingColor.MatchString(colors.Ink) || landingColor.MatchString(colors.Surface) {
		number = ballReadable(color(colors.Surface, number), disc)
	}
	if landingColor.MatchString(colors.Accent) || landingColor.MatchString(colors.Ink) {
		answer = ballReadable(color(colors.Ink, "#ffffff"), accent)
	}
	pairs := []string{
		`="#10131c"`, `="` + surface + `"`,
		`="#687289"`, `="` + border + `"`,
		`="#9eaac2"`, `="` + color(colors.Muted, "#9eaac2") + `"`,
		`="#33415b"`, `="` + color(colors.Border, "#33415b") + `"`,
		`="#f5f7fa"`, `="` + disc + `"`,
		`="#cfd5e1"`, `="` + color(colors.Border, "#cfd5e1") + `"`,
		`="#151821"`, `="` + number + `"`,
		`="#071126"`, `="` + color(colors.Surface, "#071126") + `"`,
		`="#293a66"`, `="` + color(colors.Border, "#293a66") + `"`,
		`="#174fb1"`, `="` + accent + `"`,
		`="#6f9aff"`, `="` + color(colors.Border, "#6f9aff") + `"`,
		`="#fff"`, `="` + answer + `"`,
	}
	return strings.NewReplacer(pairs...).Replace(svg)
}
func ballLuminance(color string) float64 {
	n, _ := strconv.ParseUint(strings.TrimPrefix(color, "#"), 16, 24)
	channels := []float64{float64(n>>16) / 255, float64((n>>8)&255) / 255, float64(n&255) / 255}
	for i, c := range channels {
		if c <= .04045 {
			channels[i] = c / 12.92
		} else {
			channels[i] = math.Pow((c+.055)/1.055, 2.4)
		}
	}
	return .2126*channels[0] + .7152*channels[1] + .0722*channels[2]
}
func ballReadable(ink, background string) string {
	a, b := ballLuminance(ink), ballLuminance(background)
	if (max(a, b)+.05)/(min(a, b)+.05) >= 4.5 {
		return ink
	}
	if (b+.05)/.05 >= 1.05/(b+.05) {
		return "#000000"
	}
	return "#ffffff"
}
