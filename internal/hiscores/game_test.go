package hiscores

import (
	"math"
	"testing"
)

// TestArtworkFitsWithoutCroppingOnKeyAndWideSurfaces verifies artwork fits without cropping on key and wide surfaces
func TestArtworkFitsWithoutCroppingOnKeyAndWideSurfaces(t *testing.T) {
	game := Game{Skills: []Skill{{Cape: "cape", Icon: "icon", CapeSize: [2]int{131, 255}, IconSize: [2]int{25, 25}}}}
	for _, surface := range [][2]int{{64, 64}, {112, 112}, {176, 112}, {320, 100}} {
		for _, variant := range []string{"level", "detail", "number"} {
			box := ArtworkRect(variant, surface[0] > surface[1]*13/10)
			for _, asset := range []string{"cape", "icon"} {
				size := game.Skills[0].CapeSize
				if asset == "icon" {
					size = game.Skills[0].IconSize
				}
				rect := game.FitArtwork(asset, box, surface[0], surface[1])
				if rect[2] <= 0 || rect[3] <= 0 || rect[0] < box[0] || rect[1] < box[1] || rect[0]+rect[2] > box[0]+box[2] || rect[1]+rect[3] > box[1]+box[3] {
					t.Fatal("artwork exceeds its image area", surface, asset, box, rect)
				}
				xScale := float64(rect[2]*surface[0]) / float64(200*size[0])
				yScale := float64(rect[3]*surface[1]) / float64(100*size[1])
				precision := math.Max(float64(surface[0])/float64(200*size[0]), float64(surface[1])/float64(100*size[1]))
				if math.Abs(xScale-yScale) > precision {
					t.Fatal("artwork aspect ratio was lost", surface, asset, rect)
				}
			}
		}
	}
}
