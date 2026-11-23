package randomizerdice

import (
	"fmt"
	"math"
)

// landingVariants bounds visual variation without affecting the sampled face.
const landingVariants = 4

func outwardNormal(f face) vertex {
	n := f[1].sub(f[0]).cross(f[2].sub(f[0])).unit()
	if n.dot(f[0]) < 0 {
		return n.scale(-1)
	}
	return n
}

// landedRotation faces the selected numbered facet toward the viewer, with
// the actual engraved digits or pips upright. No face marks are replaced.
func landedRotation(model dieModel, result int) func(vertex) vertex {
	for i, value := range model.marks {
		if value != result {
			continue
		}
		normal := outwardNormal(model.faces[i])
		right, up := faceAxes(normal)
		return func(p vertex) vertex {
			return vertex{p.dot(right), p.dot(up), p.dot(normal)}
		}
	}
	return func(p vertex) vertex { return p }
}

type landingMotion struct {
	axis  vertex
	turns float64
	lean  float64
}

var landingMotions = [...]landingMotion{
	{vertex{.93, .36, .06}, 1.68, .35},
	{vertex{-.58, .81, -.05}, 2.12, -.48},
	{vertex{-.94, -.33, .09}, 1.86, .51},
	{vertex{.43, -.89, .12}, 1.46, -.28},
}

func landingProgress(frame int) float64 {
	// The last sampled pose and the static result are the same frame.
	return min(1, max(0, float64(frame)/float64(rollFrameCount-1)))
}

func landingPose(frame, variant int) rollPose {
	t := landingProgress(frame)
	if t == 1 {
		return rollPose{}
	}
	direction := 1.
	if variant%2 == 1 {
		direction = -1
	}
	return rollPose{
		x: direction * .9 * math.Sin(math.Pi*t) * (1 - t),
		y: -1.6 * math.Abs(math.Sin(2*math.Pi*t)) * (1 - t),
	}
}

// landingRotation spins the rigid marked die on a tilted axis in camera
// space. The residual turn approaches zero at zero angular speed; a small
// decaying wobble changes the tumble without affecting the resting face.
func landingRotation(model dieModel, result, frame, variant int) func(vertex) vertex {
	landed := landedRotation(model, result)
	t := landingProgress(frame)
	if t == 1 {
		return landed
	}
	motion := landingMotions[variant]
	axis := motion.axis.unit()
	falloff := (1 - t) * (1 - t)
	sin, cos := math.Sincos(2 * math.Pi * motion.turns * falloff)
	wobbleSin, wobbleCos := math.Sincos(motion.lean * falloff * math.Sin(3*math.Pi*t))
	return func(p vertex) vertex {
		p = landed(p)
		// Rodrigues' formula preserves the distances between all mesh vertices.
		p = p.scale(cos).add(axis.cross(p).scale(sin)).add(axis.scale(axis.dot(p) * (1 - cos)))
		return vertex{wobbleCos*p.x - wobbleSin*p.y, wobbleSin*p.x + wobbleCos*p.y, p.z}
	}
}

// Mesh topology and engraved face assignments are identical across poses.
var landingModels = func() [21]dieModel {
	var models [21]dieModel
	for sides := int64(2); sides <= 20; sides++ {
		models[sides] = modelFor(sides)
	}
	return models
}()

func landingGlyph(sides int64, result, frame, variant int) string {
	model := landingModels[sides]
	return model.renderRotated(landingPose(frame, variant), landingRotation(model, result, frame, variant))
}

// landingDisplayGlyph adds the familiar dark result medallion only after the
// tumble and bare-pose hold complete. The mesh remains identical to frame 29.
func landingDisplayGlyph(sides int64, result, frame, variant int) string {
	glyph := landingGlyph(sides, result, frame, variant)
	if frame <= rollFrameCount+badgeHoldFrames {
		return glyph
	}
	progress := min(1.0, float64(frame-rollFrameCount-badgeHoldFrames)/float64(badgeScaleFrames))
	scale := progress * progress * (3 - 2*progress)
	return glyph + resultBadgeGlyph(result, scale, 1)
}

// Draw the same result badge for every die, fitting its outlined digits to the circle
func resultBadgeGlyph(result int, scale, opacity float64) string {
	badge := fmt.Sprintf(`<g data-result-badge="true" transform="translate(12 12) scale(%.5f) translate(-12 -12)"><circle cx="12" cy="12" r="9.2" fill="#0f141d" stroke="#f7f8fa" stroke-opacity=".75" stroke-width=".483"/><g fill="#f7f8fa">%s</g></g>`, scale, outlinedText(fmt.Sprint(result), 12, 15.335, 13.8, 9.2))
	if opacity < 1 {
		return fmt.Sprintf(`<g opacity="%.4f">%s</g>`, opacity, badge)
	}
	return badge
}

func configuredLandingGlyph(sides int64, result, frame, variant int, badge bool) string {
	if !badge {
		return landingGlyph(sides, result, frame, variant)
	}
	return landingDisplayGlyph(sides, result, frame, variant)
}
