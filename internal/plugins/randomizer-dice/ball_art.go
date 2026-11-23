package randomizerdice

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

func ballGlyph(frame int, answer string) string {
	var svg strings.Builder
	svg.WriteString(`<g>`)
	rolling := answer != "" && frame < rollFrameCount
	if rolling {
		t := float64(max(0, frame)) / float64(rollFrameCount)
		// A damped shake changes axis as it slows. At rest both amplitude
		// and velocity vanish so the answer panel has no position jump.
		amp := (1 - t) * (1 - t)
		dx := 1.55 * amp * math.Sin(9*math.Pi*t)
		dy := 1.25 * amp * math.Sin(12*math.Pi*t+.4)
		turn := 30 * amp * math.Sin(10*math.Pi*t-.6)
		fmt.Fprintf(&svg, `<g transform="translate(%.2f %.2f) rotate(%.2f 12 12)">`, dx, dy, turn)
	}
	svg.WriteString(`<circle cx="12" cy="12" r="10.1" fill="#10131c" stroke="#687289" stroke-width=".48"/><path d="M3.2 9.3a9 9 0 0 1 10.8-6.1c-4.7.1-8.6 2.4-10.8 6.1z" fill="#9eaac2" opacity=".44"/><path d="M5.1 18.3a9.2 9.2 0 0 0 14.2-1.7 9 9 0 0 1-14.2 1.7z" fill="#33415b" opacity=".72"/>`)
	if answer == "" || rolling {
		svg.WriteString(`<circle cx="12" cy="12" r="4.5" fill="#f5f7fa" stroke="#cfd5e1" stroke-width=".22"/><path d="M12 9.25c-2.17 0-2.2 2.73 0 2.73s2.17-2.73 0-2.73zm0 2.73c-2.55 0-2.57 2.88 0 2.88s2.55-2.88 0-2.88z" fill="none" stroke="#151821" stroke-width=".85"/>`)
	} else {
		svg.WriteString(`<circle cx="12" cy="12" r="8.6" fill="#071126" stroke="#293a66" stroke-width=".35"/><path d="M.8 2.2Q.3 1.2 1.5 1.2h21q1.2 0 .7 1L12.7 22q-.7 1.2-1.4 0z" fill="#174fb1" stroke="#6f9aff" stroke-width=".38" stroke-linejoin="round"/>`)
		lines := ballAnswerLines(answer)
		svg.WriteString(`<g data-answer-panel="true" fill="#fff">`)
		for i, line := range lines {
			y := 9.4 + float64(i)*3.1
			if len(lines) > 1 {
				y -= .05 + float64(len(lines)-1)*.9
			}
			width := 21 - (y-2)*1.04
			fontSize := 3.6
			svg.WriteString(outlinedText(line, 12, y, width, fontSize))
		}
		svg.WriteString(`</g>`)
	}
	if rolling {
		svg.WriteString(`</g>`)
	}
	svg.WriteString(`</g>`)
	return svg.String()
}

// Favor shorter bottom lines; the triangular window narrows below its top.
func ballAnswerLines(answer string) []string {
	words := strings.Fields(answer)
	if len(words) <= 1 || utf8.RuneCountInString(answer) <= 8 {
		return []string{answer}
	}
	best := []string{answer}
	bestCost := math.Inf(1)
	score := func(lines []string) {
		cost := 0.
		for i, line := range lines {
			cost = max(cost, float64(utf8.RuneCountInString(line))/(12-2.5*float64(i)))
		}
		cost += .12 * float64(len(lines)-2)
		if cost < bestCost {
			best, bestCost = lines, cost
		}
	}
	for i := 1; i < len(words); i++ {
		score([]string{strings.Join(words[:i], " "), strings.Join(words[i:], " ")})
		for j := i + 1; j < len(words); j++ {
			score([]string{strings.Join(words[:i], " "), strings.Join(words[i:j], " "), strings.Join(words[j:], " ")})
		}
	}
	return best
}

// Match the physical tile's aspect ratio before the host stretches its image
// element. All transforms below are uniform, so the ball never becomes an oval.
func ballWideSVG(width, height, frame int, answer string) string {
	w := 100 * float64(width) / float64(height)
	ball := min(76., w*.43)
	left := ball + 7
	panel := w - left - 4
	label := answer
	if answer == "" {
		label = "Ask a question"
	} else if frame < rollFrameCount {
		label = "Shaking"
	}
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.4f 100" data-wide-ball="true"><g transform="translate(2 %.4f) scale(%.6f)">`, w, (100-ball)/2, ball/32)
	// Retain the same shake pose, but keep the round eight face beside the
	// answer instead of drawing a second copy of it inside a stretched triangle.
	ballAnswer := ""
	if answer != "" && frame < rollFrameCount {
		ballAnswer = answer
	}
	svg.WriteString(`<g transform="translate(4 4)">` + ballGlyph(frame, ballAnswer) + `</g></g>`)
	fmt.Fprintf(&svg, `<rect x="%.4f" y="20" width="%.4f" height="60" rx="7" fill="#174fb1" stroke="#6f9aff" stroke-width=".6"/><g data-wide-answer="true" fill="#fff">`, left, panel)
	lines := ballWideLines(label)
	for i, line := range lines {
		baseline := 54. + (float64(i)-float64(len(lines)-1)/2)*16
		svg.WriteString(outlinedText(line, left+panel/2, baseline, panel-10, 16))
	}
	svg.WriteString(`</g></svg>`)
	return svg.String()
}

func ballWideLines(text string) []string {
	words := strings.Fields(text)
	if len(words) < 2 || len(text) <= 9 {
		return []string{text}
	}
	best, cost := 1, math.Inf(1)
	for i := 1; i < len(words); i++ {
		a, b := strings.Join(words[:i], " "), strings.Join(words[i:], " ")
		measure := func(s string) float64 {
			width := 0.
			for _, r := range s {
				width += answerGlyphs[r].width
			}
			return width
		}
		if next := max(measure(a), measure(b)); next < cost {
			best, cost = i, next
		}
	}
	return []string{strings.Join(words[:best], " "), strings.Join(words[best:], " ")}
}
