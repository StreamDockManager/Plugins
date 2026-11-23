package randomizerdice

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// These representative solids have their stated face counts. Only this finite
// set of meshes is packaged, even when a binding uses a million-sided die.
var highDieShapes = [...]int64{24, 30, 48, 60, 100, 120}

type vertex struct{ x, y, z float64 }
type face []vertex

func (a vertex) add(b vertex) vertex    { return vertex{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vertex) sub(b vertex) vertex    { return vertex{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vertex) scale(k float64) vertex { return vertex{a.x * k, a.y * k, a.z * k} }
func (a vertex) dot(b vertex) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vertex) unit() vertex           { return a.scale(1 / math.Sqrt(a.dot(a))) }
func (a vertex) cross(b vertex) vertex {
	return vertex{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}

func cubeFaces() []face {
	corners := []vertex{
		{-1, -1, -1}, {1, -1, -1}, {1, 1, -1}, {-1, 1, -1},
		{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1},
	}
	for i := range corners {
		corners[i] = corners[i].unit()
	}
	var faces []face
	for _, f := range [][4]int{{0, 1, 2, 3}, {4, 5, 6, 7}, {0, 1, 5, 4}, {3, 2, 6, 7}, {0, 3, 7, 4}, {1, 2, 6, 5}} {
		faces = append(faces, face{corners[f[0]], corners[f[1]], corners[f[2]], corners[f[3]]})
	}
	return faces
}

// Join staggered latitude rings with triangles and cap both poles.
func ringFaces(count int, heights []float64) []face {
	var faces []face
	var previous []vertex
	for i, height := range heights {
		ring := make([]vertex, count)
		radius := math.Sqrt(1 - height*height)
		for j := range ring {
			angle := (float64(j) + float64(i%2)/2) * 2 * math.Pi / float64(count)
			sin, cos := math.Sincos(angle)
			ring[j] = vertex{radius * cos, height, radius * sin}
		}
		for j, a := range ring {
			k := (j + 1) % count
			if previous == nil {
				faces = append(faces, face{vertex{0, 1, 0}, a, ring[k]})
			} else {
				faces = append(faces, face{a, ring[k], previous[(j+i%2)%count]},
					face{previous[j], previous[k], ring[(k-i%2+count)%count]})
			}
			if i == len(heights)-1 {
				faces = append(faces, face{a, ring[k], vertex{0, -1, 0}})
			}
		}
		previous = ring
	}
	return faces
}

func highDieMesh(sides int64) []face {
	if sides == 100 {
		return ringFaces(10, []float64{.82, .45, 0, -.45, -.82})
	}
	var base []face
	if sides == 24 || sides == 48 {
		base = cubeFaces()
	} else {
		h := 1 / math.Sqrt(5)
		base = ringFaces(5, []float64{h, -h}) // Regular icosahedron.
	}
	var faces []face
	neighbors := make(map[[2]vertex]vertex)
	for _, f := range base {
		var sum vertex
		for _, p := range f {
			sum = sum.add(p)
		}
		center := sum.unit()
		for i, a := range f {
			b := f[(i+1)%len(f)]
			switch sides {
			case 30:
				// Each icosahedron edge is a diagonal of one rhombus. The
				// adjacent face centers form the other diagonal.
				mid := a.add(b).scale(.5)
				tip := center.scale(mid.dot(mid) / mid.dot(center))
				if other, ok := neighbors[[2]vertex{a, b}]; ok {
					faces = append(faces, face{a, other, b, tip})
				} else {
					neighbors[[2]vertex{a, b}] = tip
					neighbors[[2]vertex{b, a}] = tip
				}
			case 48, 120:
				mid := a.add(b).unit()
				faces = append(faces, face{a, mid, center}, face{mid, b, center})
			default:
				faces = append(faces, face{a, b, center})
			}
		}
	}
	return faces
}

// Project the visible faces under a fixed light. Accent-colored facets and
// lighter glints follow the device palette.
func dieRotation(angle float64) func(vertex) vertex {
	turn := angle * math.Pi / 180
	yawSin, yawCos := math.Sincos(turn - .48)
	pitchSin, pitchCos := math.Sincos(.28 + .62*math.Sin(turn))
	rollSin, rollCos := math.Sincos(-.16 + .55*math.Sin(turn))
	return func(p vertex) vertex {
		x, z := yawCos*p.x+yawSin*p.z, yawCos*p.z-yawSin*p.x
		y, z := pitchCos*p.y-pitchSin*z, pitchSin*p.y+pitchCos*z
		return vertex{rollCos*x - rollSin*y, rollSin*x + rollCos*y, z}
	}
}

func (m dieModel) render(pose rollPose) string {
	return m.renderRotated(pose, dieRotation(pose.angle))
}

func (m dieModel) renderRotated(pose rollPose, rotate func(vertex) vertex) string {
	light := vertex{-.5, .72, .48}.unit()
	var svg strings.Builder
	svg.WriteString(`<g fill="#c61e32" data-sdm-fill="accent" stroke="#f5f7fa" data-sdm-stroke="ink" stroke-width=".25" stroke-linejoin="round">`)
	for i, f := range m.faces {
		normal := outwardNormal(f)
		front := rotate(normal)
		// Suppress edge-on faces whose depth sign can differ by floating-point roundoff
		if front.z <= 1e-12 {
			continue
		}
		lit := max(0, front.dot(light))
		var outline strings.Builder
		outline.WriteByte('M')
		for _, p := range f {
			v := rotate(p)
			fmt.Fprintf(&outline, "%.2f %.2f ", 12+pose.x+12.4*v.x, 12+pose.y-12.4*v.y)
		}
		outline.WriteByte('z')
		fmt.Fprintf(&svg, `<path fill-opacity="%.2f" stroke-opacity=".42" d="%s"/>`, .34+.62*lit, outline.String())
		if lit > .45 {
			fmt.Fprintf(&svg, `<path fill="#f5f7fa" data-sdm-fill="ink" fill-opacity="%.2f" stroke="none" d="%s"/>`, 1.05*(lit-.45), outline.String())
		}
		if len(m.marks) != 0 && front.z > .06 {
			m.engraveFace(&svg, i, normal, pose, rotate)
		}
	}
	svg.WriteString("</g>")
	if len(m.marks) == 0 {
		engraveDie(&svg, int(m.sides), pose, rotate)
	}
	return svg.String()
}

func engraveDie(svg *strings.Builder, sides int, pose rollPose, rotate func(vertex) vertex) {
	// Four sparse number marks rotate on the surface. Each is at most
	// two thirds of the modeled face count, also valid for nearby custom dice.
	for i, value := range []int{7, sides / 3, sides / 2, sides * 2 / 3} {
		sin, cos := math.Sincos(float64(i)*math.Pi/2 + .24)
		height := .28
		if i%2 == 1 {
			height = -height
		}
		n := vertex{cos, height, sin}.unit()
		front := rotate(n)
		opacity := min(1, max(0, (front.z-.2)*2)) * (1 - pose.reveal)
		if opacity < .01 {
			continue
		}
		right := vertex{sin, 0, -cos}
		up := rotate(n.cross(right))
		right = rotate(right)
		fmt.Fprintf(svg, `<g transform="matrix(%.3f %.3f %.3f %.3f %.2f %.2f)" opacity="%.2f" fill="none" stroke="#f5f7fa" data-sdm-stroke="ink">`,
			right.x, -right.y, -up.x, up.y, 12+pose.x+10.6*front.x, 12+pose.y-10.6*front.y, opacity)
		writeNumeral(svg, value, 1)
		svg.WriteString("</g>")
	}
}

func writeNumeral(svg *strings.Builder, value int, scale float64) {
	fmt.Fprintf(svg, `<g transform="scale(%.3f)" stroke-width=".60">`, scale)
	digits := strconv.Itoa(value)
	for j, digit := range digits {
		x := (float64(j)-float64(len(digits)-1)/2)*2.8 - 1
		fmt.Fprintf(svg, `<path transform="translate(%.1f -1.7)" d="%s"/>`, x, numeralPaths[digit-'0'])
	}
	svg.WriteString("</g>")
}

// Engraved digits are paths so the packaged SVGs need no installed font.
var numeralPaths = [...]string{
	"M.5 0h1L2 .5v2.4l-.5 .5h-1L0 2.9V.5z",
	"M.2 .7 1.2 0v3.4",
	"M0 .5 .5 0h1l.5 .5v.7L0 3.4h2",
	"M0 0h1.6l.4 .5v.7l-.6 .5.6 .5v.7l-.5 .5H0M.7 1.7h.7",
	"M1.6 3.4V0L0 2.1h2.2",
	"M2 0H0v1.5h1.5l.5 .5v.9l-.5 .5H0",
	"M1.8 0H.6L0 .7v2.2l.5 .5h1l.5-.5v-.8l-.5-.5H0",
	"M0 0h2L.6 3.4",
	"M.5 0h1l.5 .5v.7l-.5 .5h-1l-.5-.5V.5zM.5 1.7l-.5 .5v.7l.5 .5h1l.5-.5v-.7l-.5-.5",
	"M2 1.8H.5L0 1.3V.5L.5 0h1l.5 .5v2.2l-.6 .7H.2",
}
