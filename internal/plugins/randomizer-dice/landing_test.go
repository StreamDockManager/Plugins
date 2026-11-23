package randomizerdice

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestLandedDiceFaceAndRotation(t *testing.T) {
	for sides := int64(2); sides <= 20; sides++ {
		model := modelFor(sides)
		for result := 1; result <= int(sides); result++ {
			base := landingGlyph(sides, result, rollFrameCount, 0)
			for variant := range landingVariants {
				final := landingRotation(model, result, rollFrameCount, variant)
				best, bestScore := -1, -2.0
				for i, f := range model.faces {
					score := final(outwardNormal(f)).z
					if score > bestScore {
						best, bestScore = i, score
					}
				}
				if model.marks[best] != result || math.Abs(bestScore-1) > 1e-9 {
					t.Fatalf("d%d result %d variant %d: front face is %d (%.6f)", sides, result, variant, model.marks[best], bestScore)
				}
				if base != landingGlyph(sides, result, rollFrameCount-1, variant) ||
					base != landingGlyph(sides, result, rollFrameCount, variant) {
					t.Fatalf("d%d result %d variant %d changed artwork at rest", sides, result, variant)
				}
				old := landingRotation(model, result, rollFrameCount-2, variant)
				for _, v := range model.faces[best] {
					delta := old(v).sub(final(v))
					if delta.dot(delta) > .0005 {
						t.Fatalf("d%d result %d snapped into final pose", sides, result)
					}
				}
			}
		}
	}
}

func TestCoinFlipsAndRollMotionVaries(t *testing.T) {
	model := modelFor(2)
	front := outwardNormal(model.faces[24])
	back := outwardNormal(model.faces[25])
	if front.dot(back) > -.99999 {
		t.Fatal("coin marks are not on opposite caps")
	}
	poses := map[vertex]bool{}
	for variant := range landingVariants {
		sawFront, sawBack, sawEdge := false, false, false
		for frame := range rollFrameCount {
			rot := landingRotation(model, 2, frame, variant)
			n := rot(front)
			if math.Abs(n.dot(n)-1) > 1e-9 || math.Abs(n.dot(rot(back))+1) > 1e-9 {
				t.Fatal("rotation deformed the coin")
			}
			sawFront = sawFront || n.z > .85
			sawBack = sawBack || n.z < -.85
			sawEdge = sawEdge || math.Abs(n.z) < .18
			if frame == 9 {
				poses[n] = true
			}
		}
		if !sawFront || !sawBack || !sawEdge {
			t.Fatalf("variant %d is not a visible coin flip", variant)
		}
	}
	if len(poses) != landingVariants {
		t.Fatal("roll variants have matching mid-roll pose")
	}
}

func TestResultBadgeOnlyAfterLanding(t *testing.T) {
	for sides := int64(2); sides <= 20; sides++ {
		for result := 1; result <= int(sides); result++ {
			for variant := range landingVariants {
				for frame := 0; frame <= rollFrameCount+badgeHoldFrames; frame++ {
					if strings.Contains(landingDisplayGlyph(sides, result, frame, variant), "data-result-badge") {
						t.Fatal("premature badge", sides, result, frame)
					}
				}
				final := landingDisplayGlyph(sides, result, badgeFinalFrame, variant)
				if !strings.HasPrefix(final, landingGlyph(sides, result, rollFrameCount-1, variant)) {
					t.Fatal("mesh moved on reveal", sides, result)
				}
				if strings.Count(final, "data-result-badge") != 1 || !strings.Contains(final, fmt.Sprintf(`data-outline-text="%d"`, result)) {
					t.Fatal("incorrect result badge", sides, result)
				}
			}
		}
	}
}

func TestBadgeEasesFromCenterWithFrozenMesh(t *testing.T) {
	mesh := landingGlyph(6, 4, rollFrameCount-1, 0)
	for step := 1; step <= badgeScaleFrames; step++ {
		frame := rollFrameCount + badgeHoldFrames + step
		svg := landingDisplayGlyph(6, 4, frame, 0)
		p := float64(step) / float64(badgeScaleFrames)
		scale := p * p * (3 - 2*p)
		if !strings.HasPrefix(svg, mesh) || !strings.Contains(svg, fmt.Sprintf("translate(12 12) scale(%.5f) translate(-12 -12)", scale)) {
			t.Fatal("incorrect center scale or moved mesh", step)
		}
	}
}
