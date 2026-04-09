package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IntegrateParts moves extracted assets and returns tracking info for Undo functionality
func IntegrateParts(assets *KiCadAssets, category string, targetRepoRoot string, repoName string) ([]string, string, string, error) {

	prettyFolder := filepath.Join(targetRepoRoot, "footprints", fmt.Sprintf("%s.pretty", category))
	shapesFolder := filepath.Join(targetRepoRoot, "packages3d", fmt.Sprintf("%s.3dshapes", category))
	symbolsFolder := filepath.Join(targetRepoRoot, "symbols")
	blocksFolder := filepath.Join(targetRepoRoot, "blocks", category)

	os.MkdirAll(prettyFolder, os.ModePerm)
	os.MkdirAll(shapesFolder, os.ModePerm)
	os.MkdirAll(symbolsFolder, os.ModePerm)
	os.MkdirAll(blocksFolder, os.ModePerm)

	var finalModelName string
	var addedFiles []string
	var masterSym string
	var backupSym string

	// 1. Handle 3D Models
	if assets.ModelPath != "" {
		finalModelName = filepath.Base(assets.ModelPath)
		destModelPath := filepath.Join(shapesFolder, finalModelName)
		copyFile(assets.ModelPath, destModelPath)
		addedFiles = append(addedFiles, destModelPath)
		fmt.Println("--> Copied 3D Model to:", destModelPath)
	}

	// 2. Handle Footprints
	var finalFootprintName string
	if assets.FootprintPath != "" {
		finalFootprintName = strings.TrimSuffix(filepath.Base(assets.FootprintPath), ".kicad_mod")
		destFootprintPath := filepath.Join(prettyFolder, filepath.Base(assets.FootprintPath))

		if finalModelName != "" {
			patchFootprint3DPath(assets.FootprintPath, destFootprintPath, category, finalModelName, repoName)
			fmt.Println("--> Copied & Patched Footprint to:", destFootprintPath)
		} else {
			copyFile(assets.FootprintPath, destFootprintPath)
			fmt.Println("--> Copied Footprint to:", destFootprintPath)
		}
		addedFiles = append(addedFiles, destFootprintPath)

		// Usability: Auto-register this category's footprint library in all detected KiCad versions
		UpdateKiCadFpTable(category, prettyFolder)
	}

	// 3. Handle Symbols
	if assets.SymbolPath != "" {
		masterSym = filepath.Join(symbolsFolder, fmt.Sprintf("%s.kicad_sym", category))
		backupSym = masterSym + ".bak"

		if _, err := os.Stat(masterSym); err == nil {
			copyFile(masterSym, backupSym)
		}

		injectSymbol(assets.SymbolPath, masterSym, category, finalFootprintName)
		fmt.Println("--> Injected & Sanitized Symbol into:", masterSym)

		// Usability: Auto-register this category's symbol library in all detected KiCad versions
		UpdateKiCadSymTable(category, masterSym)
	}

	// 4. Handle Design Blocks
	if assets.SchBlockPath != "" {
		destSch := filepath.Join(blocksFolder, filepath.Base(assets.SchBlockPath))
		copyFile(assets.SchBlockPath, destSch)
		addedFiles = append(addedFiles, destSch)
		fmt.Println("--> Copied Schematic Design Block to:", destSch)
	}
	if assets.PcbBlockPath != "" {
		destPcb := filepath.Join(blocksFolder, filepath.Base(assets.PcbBlockPath))
		copyFile(assets.PcbBlockPath, destPcb)
		addedFiles = append(addedFiles, destPcb)
		fmt.Println("--> Copied PCB Design Block to:", destPcb)
	}

	return addedFiles, masterSym, backupSym, nil
}

// InitializeKiCadLibraries pre-registers all default categories in KiCad's global tables.
// This ensures they appear in the KiCad UI immediately on first launch.
func InitializeKiCadLibraries(conf Config) {
	if conf.BaseLibPath == "" || len(conf.Repositories) == 0 {
		return
	}

	fmt.Println("--> Performing first-launch KiCad library registration...")

	// Default to the first repository for initial registration
	defaultRepo := conf.Repositories[0].Name
	targetRepoRoot := filepath.Join(conf.BaseLibPath, defaultRepo)

	for _, category := range conf.Categories {
		// 1. Setup Footprint Library Folder
		prettyPath := filepath.Join(targetRepoRoot, "footprints", fmt.Sprintf("%s.pretty", category))
		os.MkdirAll(prettyPath, os.ModePerm)
		UpdateKiCadFpTable(category, prettyPath)

		// 2. Setup Symbol Library File
		symDir := filepath.Join(targetRepoRoot, "symbols")
		symPath := filepath.Join(symDir, fmt.Sprintf("%s.kicad_sym", category))

		if _, err := os.Stat(symPath); os.IsNotExist(err) {
			os.MkdirAll(symDir, os.ModePerm)
			// Initialize with a valid empty KiCad Symbol Library header
			emptyLib := "(kicad_symbol_lib (version 20211014) (generator kicad_symbol_editor)\n)\n"
			os.WriteFile(symPath, []byte(emptyLib), 0644)
		}
		UpdateKiCadSymTable(category, symPath)
	}
}

func injectSymbol(sourceFile, masterFile, category, footprintName string) error {
	srcBytes, err := os.ReadFile(sourceFile)
	if err != nil {
		return err
	}
	srcContent := string(srcBytes)

	reSymbolBlock := regexp.MustCompile(`(?s)\(\s*symbol\s+".+`)
	match := reSymbolBlock.FindString(srcContent)
	if match == "" {
		return fmt.Errorf("could not find a valid (symbol ...) block in source file")
	}

	lastParenIdx := strings.LastIndex(match, ")")
	if lastParenIdx == -1 {
		return fmt.Errorf("malformed source symbol file")
	}
	extractedSymbol := strings.TrimSpace(match[:lastParenIdx])

	if footprintName != "" {
		reFootprintProp := regexp.MustCompile(`\(property\s+"Footprint"\s+"[^"]*"`)
		newProp := fmt.Sprintf(`(property "Footprint" "%s:%s"`, category, footprintName)
		extractedSymbol = reFootprintProp.ReplaceAllString(extractedSymbol, newProp)
	}

	var masterContent string
	if _, err := os.Stat(masterFile); os.IsNotExist(err) {
		masterContent = `(kicad_symbol_lib (version 20211014) (generator kicad_symbol_editor)
)`
	} else {
		masterBytes, err := os.ReadFile(masterFile)
		if err != nil {
			return err
		}
		masterContent = string(masterBytes)
	}

	masterLastParenIdx := strings.LastIndex(masterContent, ")")
	if masterLastParenIdx == -1 {
		return fmt.Errorf("master symbol file is malformed")
	}

	newMasterContent := masterContent[:masterLastParenIdx] + "\n  " + extractedSymbol + "\n)\n"
	return os.WriteFile(masterFile, []byte(newMasterContent), 0644)
}

func patchFootprint3DPath(src, dest, category, modelFileName, repoName string) error {
	contentBytes, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	re := regexp.MustCompile(`(?i)\(model\s+"?([^"\)]+\.(?:step|stp|wrl))"?`)
	newModelPath := fmt.Sprintf(`(model "${KICAD_USER_3DMODEL_DIR}/%s/packages3d/%s.3dshapes/%s"`, repoName, category, modelFileName)
	patchedContent := re.ReplaceAllString(content, newModelPath)

	return os.WriteFile(dest, []byte(patchedContent), 0644)
}

func copyFile(src, dest string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func UpdateKiCadSymTable(libNickname, libPath string) error {
	configDir, _ := os.UserConfigDir()
	kicadBase := filepath.Join(configDir, "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	versionRegex := regexp.MustCompile(`^\d+(\.\d+)?$`)

	for _, entry := range entries {
		if !entry.IsDir() || !versionRegex.MatchString(entry.Name()) {
			continue
		}

		tablePath := filepath.Join(kicadBase, entry.Name(), "sym-lib-table")

		content, err := os.ReadFile(tablePath)
		if err != nil {
			content = []byte("(sym_lib_table\n)")
		}

		sContent := string(content)
		if strings.Contains(sContent, fmt.Sprintf("(name %q)", libNickname)) {
			continue
		}

		entryStr := fmt.Sprintf("  (lib (name %q)(type \"KiCad\")(uri %q)(options \"\")(descr \"Added by KiCadLibMgr\"))\n", libNickname, libPath)
		lastIdx := strings.LastIndex(sContent, ")")
		if lastIdx == -1 {
			continue
		}

		newContent := sContent[:lastIdx] + entryStr + ")\n"
		os.WriteFile(tablePath, []byte(newContent), 0644)
		fmt.Printf("--> Registered symbol library %s in KiCad %s\n", libNickname, entry.Name())
	}
	return nil
}

func UpdateKiCadFpTable(libNickname, libPath string) error {
	configDir, _ := os.UserConfigDir()
	kicadBase := filepath.Join(configDir, "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	versionRegex := regexp.MustCompile(`^\d+(\.\d+)?$`)

	for _, entry := range entries {
		if !entry.IsDir() || !versionRegex.MatchString(entry.Name()) {
			continue
		}

		tablePath := filepath.Join(kicadBase, entry.Name(), "fp-lib-table")

		content, err := os.ReadFile(tablePath)
		if err != nil {
			content = []byte("(fp_lib_table\n)")
		}

		sContent := string(content)
		if strings.Contains(sContent, fmt.Sprintf("(name %q)", libNickname)) {
			continue
		}

		entryStr := fmt.Sprintf("  (lib (name %q)(type \"KiCad\")(uri %q)(options \"\")(descr \"Added by KiCadLibMgr\"))\n", libNickname, libPath)
		lastIdx := strings.LastIndex(sContent, ")")
		if lastIdx == -1 {
			continue
		}

		newContent := sContent[:lastIdx] + entryStr + ")\n"
		os.WriteFile(tablePath, []byte(newContent), 0644)
		fmt.Printf("--> Registered footprint library %s in KiCad %s\n", libNickname, entry.Name())
	}
	return nil
}
