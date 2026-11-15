package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAssetsRejectSVGContentThatNativeRenderingWouldOmit verifies assets reject SVG content that native rendering would omit
func TestAssetsRejectSVGContentThatNativeRenderingWouldOmit(t *testing.T) {
	definition := Definitions[0]
	for _, source := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"><image href="data:image/png;base64,fixture"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><image href="art.png"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><div>Label</div></foreignObject></svg>`,
		`<svg><path></svg>`,
	} {
		files := PluginAssets(definition)
		files["icon.svg"] = []byte(source)
		if err := ValidateAssets(definition, files); err == nil {
			t.Fatal("unrenderable SVG was accepted", source)
		}
	}
}

// TestEveryFeatureDeclaresItsDynamicAssets verifies every feature declares its dynamic assets
func TestEveryFeatureDeclaresItsDynamicAssets(t *testing.T) {
	reference := regexp.MustCompile(`"(icons/[a-z-]+)"`)
	for _, d := range Definitions {
		files := PluginAssets(d)
		if err := ValidateAssets(d, files); err != nil {
			t.Fatal(err)
		}
		source, err := filepath.Glob(filepath.Join("../plugins", d.Slug, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range source {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range reference.FindAllSubmatch(data, -1) {
				if _, ok := files[string(match[1])+".svg"]; !ok {
					t.Errorf("%s runtime references unshipped %s", d.Slug, match[1])
				}
			}
		}
		for _, path := range RequiredAssets(d) {
			name := path
			if !explicitAsset(path) {
				name = ThemeAsset(path+".svg", "dark")
			}
			if _, ok := files[name]; !ok {
				t.Errorf("missing themed runtime asset: %s", path)
			}
		}
		if d.Slug == "counter-tally" {
			for _, name := range []string{"icons/plus.svg", "icons/minus.svg", "icons/reset.svg", "icons/unavailable.svg"} {
				if _, ok := files[name]; !ok {
					t.Fatal(name)
				}
			}
			for _, name := range []string{"icons/cpu.svg", "icons/request.svg", "icons/microphone.svg", "icons/busy.svg"} {
				if _, ok := files[name]; ok {
					t.Fatal("unrelated Counter asset", name)
				}
			}
		}
	}
}
