package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setupKiCadConfig points the app and "KiCad" at a throwaway config dir with a
// KiCad 10.0 profile, and resets the cached app config so defaults are used.
func setupKiCadConfig(t *testing.T) (configDir, kicadProfile string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("config dir redirection via XDG_CONFIG_HOME is Linux-only")
	}
	configDir = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	configCacheMu.Lock()
	configCache = nil
	configCacheMu.Unlock()
	t.Cleanup(func() {
		configCacheMu.Lock()
		configCache = nil
		configCacheMu.Unlock()
	})

	kicadProfile = filepath.Join(configDir, "kicad", "10.0")
	if err := os.MkdirAll(kicadProfile, 0o755); err != nil {
		t.Fatal(err)
	}
	return configDir, kicadProfile
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestModelOnlyImportRegistersNewCategory(t *testing.T) {
	_, profile := setupKiCadConfig(t)
	src := t.TempDir()
	model := filepath.Join(src, "CMM-2718AT-42308-TR.STEP")
	writeFile(t, model, "ISO-10303-21;\nEND-ISO-10303-21;\n")

	repoRoot := filepath.Join(t.TempDir(), "CustomLibs")
	assets := &KiCadAssets{ModelPath: model, SourcePath: filepath.Join(src, "mic.zip")}

	added, _, _, err := IntegrateParts(assets, "Microphones", repoRoot, "CustomLibs", "", "", "", "")
	if err != nil {
		t.Fatalf("IntegrateParts: %v", err)
	}

	wantModel := filepath.Join(repoRoot, "packages3d", "Microphones.3dshapes", "CMM-2718AT-42308-TR.STEP")
	if len(added) != 1 || added[0] != wantModel {
		t.Fatalf("added files = %v, want [%s]", added, wantModel)
	}

	symLib := filepath.Join(repoRoot, "symbols", "Microphones.kicad_sym")
	if got := readFile(t, symLib); got != emptySymbolLib {
		t.Errorf("symbol library content = %q, want empty library", got)
	}

	for table, uri := range map[string]string{
		"sym-lib-table":          symLib,
		"fp-lib-table":           filepath.Join(repoRoot, "footprints", "Microphones.pretty"),
		"design-block-lib-table": filepath.Join(repoRoot, "blocks", "Microphones.kicad_blocks"),
	} {
		content := readFile(t, filepath.Join(profile, table))
		if !strings.Contains(content, `(name "Microphones")`) || !strings.Contains(content, uri) {
			t.Errorf("%s does not register Microphones at %s:\n%s", table, uri, content)
		}
	}
}

func TestImportRegistersTablesOnlyOnce(t *testing.T) {
	_, profile := setupKiCadConfig(t)
	repoRoot := filepath.Join(t.TempDir(), "CustomLibs")
	src := t.TempDir()
	model := filepath.Join(src, "a.step")
	writeFile(t, model, "x")

	for i := 0; i < 2; i++ {
		if _, _, _, err := IntegrateParts(&KiCadAssets{ModelPath: model}, "Microphones", repoRoot, "CustomLibs", "overwrite", "", "", ""); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	for _, table := range []string{"sym-lib-table", "fp-lib-table"} {
		if n := strings.Count(readFile(t, filepath.Join(profile, table)), `(name "Microphones")`); n != 1 {
			t.Errorf("%s registers Microphones %d times, want 1", table, n)
		}
	}
}

func TestSnapMagicImportWithUnquotedFootprintName(t *testing.T) {
	setupKiCadConfig(t)
	src := t.TempDir()
	sym := filepath.Join(src, "CMM-2718AT-42308-TR.kicad_sym")
	writeFile(t, sym, `(kicad_symbol_lib (version 20211014) (generator kicad_symbol_editor)
  (symbol "CMM-2718AT-42308-TR" (in_bom yes) (on_board yes)
    (property "Reference" "U" (id 0) (at 0 0 0))
    (property "Footprint" "XDCR_CMM-2718AT-42308-TR" (id 2) (at 0 0 0))
  )
)
`)
	fp := filepath.Join(src, "XDCR_CMM-2718AT-42308-TR.kicad_mod")
	writeFile(t, fp, "\n(footprint XDCR_CMM-2718AT-42308-TR (layer F.Cu) (tedit 6B04BB37)\n  (attr smd)\n)\n")
	model := filepath.Join(src, "CMM-2718AT-42308-TR.step")
	writeFile(t, model, "x")

	repoRoot := filepath.Join(t.TempDir(), "CustomLibs")
	assets := &KiCadAssets{SymbolPath: sym, FootprintPath: fp, ModelPath: model}
	if _, _, _, err := IntegrateParts(assets, "Microphones", repoRoot, "CustomLibs", "", "", "", ""); err != nil {
		t.Fatalf("IntegrateParts: %v", err)
	}

	got := readFile(t, filepath.Join(repoRoot, "footprints", "Microphones.pretty", "CMM-2718AT-42308-TR.kicad_mod"))
	if !strings.Contains(got, `(footprint "CMM-2718AT-42308-TR" (layer F.Cu)`) {
		t.Errorf("footprint name not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "${KICAD_USER_3DMODEL_DIR}/CustomLibs/packages3d/Microphones.3dshapes/CMM-2718AT-42308-TR.step") {
		t.Errorf("3D model path not added:\n%s", got)
	}

	lib := readFile(t, filepath.Join(repoRoot, "symbols", "Microphones.kicad_sym"))
	if !strings.Contains(lib, `(symbol "CMM-2718AT-42308-TR"`) {
		t.Errorf("symbol not injected:\n%s", lib)
	}
	if !strings.Contains(lib, `(property "Footprint" "Microphones:CMM-2718AT-42308-TR"`) {
		t.Errorf("symbol footprint field not linked:\n%s", lib)
	}
}

func TestRenameFootprint(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"quoted", `(footprint "Old_Name" (version 20240108)`, `(footprint "New" (version 20240108)`},
		{"unquoted", "(footprint XDCR_Old-1 (layer F.Cu)", `(footprint "New" (layer F.Cu)`},
		{"leading blank line", "\n(footprint Old (layer F.Cu)", "\n" + `(footprint "New" (layer F.Cu)`},
		{"legacy module", `(module "Old" (layer F.Cu)`, `(module "New" (layer F.Cu)`},
		{"legacy unquoted module", "(module Old:Pkg (layer F.Cu)", `(module "New" (layer F.Cu)`},
		{"quoted with escaped quote", `(footprint "Old \"x\"" (layer F.Cu)`, `(footprint "New" (layer F.Cu)`},
		{"only first s-expression", "(footprint Old\n  (fp_text value Old)", "(footprint \"New\"\n  (fp_text value Old)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renameFootprint(c.in, "New"); got != c.want {
				t.Errorf("renameFootprint(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// Names containing regex replacement syntax or quotes must be written literally.
	if got := renameFootprint("(footprint Old (layer F.Cu)", `A$1"B`); got != `(footprint "A$1\"B" (layer F.Cu)` {
		t.Errorf("special characters mangled: %q", got)
	}
}
