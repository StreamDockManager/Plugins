package randomizerdice

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type dieModel struct {
	coin  bool
	sides int64
	faces []face
	marks []int // Number on each face. Caps and coin edges can be unmarked.
}

// Face order follows cubeFaces: back, front, bottom, top, left, right.
// These are rotations of one die, with opposite faces adding up to seven.
var cubeMarks = [...][6]int{
	{6, 1, 4, 3, 5, 2}, {5, 2, 4, 3, 1, 6}, {4, 3, 5, 2, 6, 1},
	{3, 4, 2, 5, 6, 1}, {2, 5, 4, 3, 6, 1}, {1, 6, 4, 3, 2, 5},
}

func modelFor(sides int64) dieModel {
	m := dieModel{sides: sides}
	switch sides {
	case 2:
		m.faces = prismFaces(24, true)
		m.marks = make([]int, len(m.faces))
		m.marks[24], m.marks[25] = 1, 2
		return m
	case 4:
		// Three edges on the base and three equal edges up to the apex.
		base := make(face, 3)
		for i := range base {
			sin, cos := math.Sincos(1.08 + float64(i)*2*math.Pi/3)
			base[i] = vertex{math.Sqrt(8.0/9) * cos, -1.0 / 3, math.Sqrt(8.0/9) * sin}
		}
		m.faces = []face{base}
		for i, p := range base {
			m.faces = append(m.faces, face{{y: 1}, p, base[(i+1)%3]})
		}
	case 6:
		m.faces = cubeFaces()
		m.marks = cubeMarks[4][:]
		return m
	case 8:
		m.faces = ringFaces(4, []float64{0})
	case 12, 20:
		h := 1 / math.Sqrt(5)
		m.faces = ringFaces(5, []float64{h, -h})
		if sides == 12 {
			m.faces = dualFaces(m.faces)
		}
	default:
		if sides > 20 {
			m.faces = highDieMesh(sides)
			return m
		}
		if sides%2 == 0 {
			m.faces = kiteFaces(int(sides))
		} else {
			m.faces = prismFaces(int(sides), false)
		}
	}
	m.marks = make([]int, len(m.faces))
	for i := range int(sides) {
		m.marks[i] = i + 1
	}
	return m
}

// Odd dice are barrels with numbered longitudinal faces. A d2 is a shallow
// disk with two numbered caps, oriented towards the viewer at rest.
func prismFaces(count int, coin bool) []face {
	a, b := make(face, count), make(face, count)
	for i := range count {
		sin, cos := math.Sincos(float64(i) * 2 * math.Pi / float64(count))
		a[i], b[i] = vertex{.78 * cos, .71, .78 * sin}, vertex{.78 * cos, -.71, .78 * sin}
		if coin {
			a[i], b[i] = vertex{.94 * cos, .94 * sin, .14}, vertex{.94 * cos, .94 * sin, -.14}
		}
	}
	var faces []face
	for i, p := range a {
		next := (i + 1) % count
		faces = append(faces, face{p, a[next], b[next], b[i]})
	}
	return append(faces, a, b)
}

// Two poles and a zigzag equator form the d10's pentagonal trapezohedron.
// The same construction gives the other even custom dice planar kite faces.
func kiteFaces(sides int) []face {
	c := math.Cos(2 * math.Pi / float64(sides))
	height := (1 - c) / (1 + c)
	ring := make(face, sides)
	for i := range ring {
		sin, cos := math.Sincos(float64(i) * 2 * math.Pi / float64(sides))
		ring[i] = vertex{.85 * cos, height, .85 * sin}
		height = -height
	}
	var faces []face
	for i, p := range ring {
		tip := vertex{y: 1}
		if i%2 == 0 {
			tip.y = -1
		}
		faces = append(faces, face{tip, ring[(i+sides-1)%sides], p, ring[(i+1)%sides]})
	}
	return faces
}

func (f face) center() vertex {
	var sum vertex
	for _, p := range f {
		sum = sum.add(p)
	}
	return sum.scale(1 / float64(len(f)))
}

func faceAxes(n vertex) (vertex, vertex) {
	right := vertex{n.z, 0, -n.x}
	if right.dot(right) < .00001 {
		right = vertex{x: 1}
	}
	right = right.unit()
	return right, n.cross(right)
}

// The five neighboring icosahedron face centers around each vertex form
// one regular pentagon of the d12. Visit vertices in source order.
func dualFaces(source []face) []face {
	var vertices []vertex
	rings := map[vertex]face{}
	for _, f := range source {
		c := f.center().unit()
		for _, p := range f {
			if rings[p] == nil {
				vertices = append(vertices, p)
			}
			rings[p] = append(rings[p], c)
		}
	}
	var result []face
	for _, p := range vertices {
		f := rings[p]
		right, up := faceAxes(p)
		angle := func(v vertex) float64 { return math.Atan2(v.dot(up), v.dot(right)) }
		sort.Slice(f, func(i, j int) bool { return angle(f[i]) < angle(f[j]) })
		result = append(result, f)
	}
	return result
}

// Place the marks in the face's own plane so they turn and foreshorten
// with the facets. Size them to the nearest edge, including on narrow kites.
func (m dieModel) engraveFace(svg *strings.Builder, i int, normal vertex, pose rollPose, rotate func(vertex) vertex) {
	value := m.marks[i]
	if value == 0 {
		return
	}
	f := m.faces[i]
	center := f.center()
	radius := math.Inf(1)
	for j, p := range f {
		distance := center.sub(p).cross(f[(j+1)%len(f)].sub(p).unit())
		radius = min(radius, math.Sqrt(distance.dot(distance)))
	}
	right, up := faceAxes(normal)
	right, up = rotate(right.scale(12.4*radius)), rotate(up.scale(12.4*radius))
	dot := rotate(center)
	opacity := 1.0
	if m.sides != 6 {
		opacity -= pose.reveal
	}
	fmt.Fprintf(svg, `<g transform="matrix(%.3f %.3f %.3f %.3f %.2f %.2f)" opacity="%.3f" fill="none" stroke="#f5f7fa" data-sdm-stroke="ink">`, right.x, -right.y, -up.x, up.y, 12+pose.x+12.4*dot.x, 12+pose.y-12.4*dot.y, opacity)
	switch m.sides {
	case 2:
		svg.WriteString(`<circle r=".83" stroke-width=".04"/>`)
		if m.coin {
			label := "H"
			if value == 2 {
				label = "T"
			}
			svg.WriteString(`<g fill="#f5f7fa" data-sdm-fill="ink" stroke="none">` + outlinedText(label, 0, .4, 1.2, 1.1) + `</g>`)
		} else {
			writeNumeral(svg, value, .32)
		}
	case 6:
		// Horizontal and vertical spacing are fractions of the half-face width.
		svg.WriteString(`<g fill="#f5f7fa" data-sdm-fill="ink" stroke="none">`)
		for _, p := range pipPositions[value-1] {
			fmt.Fprintf(svg, `<circle cx="%.2f" cy="%.2f" r=".155"/>`, float64(p[0])*.51, float64(p[1])*.51)
		}
		svg.WriteString("</g>")
	default:
		scale := .32
		if value >= 10 {
			scale = .26
		}
		writeNumeral(svg, value, scale)
	}
	svg.WriteString("</g>")
}

var pipPositions = [...][][2]int{
	{{0, 0}},
	{{-1, -1}, {1, 1}},
	{{-1, -1}, {0, 0}, {1, 1}},
	{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}},
	{{-1, -1}, {1, -1}, {0, 0}, {-1, 1}, {1, 1}},
	{{-1, -1}, {1, -1}, {-1, 0}, {1, 0}, {-1, 1}, {1, 1}},
}
