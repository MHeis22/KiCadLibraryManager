package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// kicadVersionRegex matches KiCad version directory names like "8", "8.0", or "8.0.0"
var kicadVersionRegex = regexp.MustCompile(`^\d+(\.\d+)*$`)

// IntegrateParts moves extracted assets and returns tracking info for Undo functionality
// IntegrateParts moves extracted assets and returns tracking info for Undo functionality
func IntegrateParts(assets *KiCadAssets, category string, targetRepoRoot string, repoName string, conflictStrategy string, newName string) ([]string, string, string, error) {

	prettyFolder := filepath.Join(targetRepoRoot, "footprints", fmt.Sprintf("%s.pretty", category))
	shapesFolder := filepath.Join(targetRepoRoot, "packages3d", fmt.Sprintf("%s.3dshapes", category))
	symbolsFolder := filepath.Join(targetRepoRoot, "symbols")
	blocksLibFolder := filepath.Join(targetRepoRoot, "blocks", fmt.Sprintf("%s.kicad_blocks", category))

	for _, dir := range []string{prettyFolder, shapesFolder, symbolsFolder} {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return nil, "", "", fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	var finalModelName string
	var addedFiles []string
	var masterSym string
	var backupSym string

	// --- NEW: Auto-determine component name from symbol ---
	var autoName string
	if assets.SymbolPath != "" {
		srcBytes, _ := os.ReadFile(assets.SymbolPath)
		reSymName := regexp.MustCompile(`(?s)\(\s*symbol\s+"([^"]+)"`)
		match := reSymName.FindStringSubmatch(string(srcBytes))
		if len(match) > 1 {
			autoName = match[1]
			// Sanitize invalid filename characters just in case
			autoName = strings.ReplaceAll(autoName, "/", "_")
			autoName = strings.ReplaceAll(autoName, "\\", "_")
		}
	}

	// Manual rename from UI overrides the auto-detected name
	if conflictStrategy == "rename" && newName != "" {
		autoName = newName
	}

	// 1. Handle 3D Models
	if assets.ModelPath != "" {
		finalModelName = filepath.Base(assets.ModelPath)
		if autoName != "" {
			finalModelName = autoName + filepath.Ext(assets.ModelPath)
		}

		destModelPath := filepath.Join(shapesFolder, finalModelName)
		if err := copyFile(assets.ModelPath, destModelPath); err != nil {
			fmt.Println("Warning: failed to copy 3D model:", err)
		} else {
			addedFiles = append(addedFiles, destModelPath)
			fmt.Println("--> Copied 3D Model to:", destModelPath)
		}
	}

	// 2. Handle Footprints
	var finalFootprintName string
	if assets.FootprintPath != "" {
		baseName := filepath.Base(assets.FootprintPath)
		if autoName != "" {
			baseName = autoName + ".kicad_mod"
		}

		finalFootprintName = strings.TrimSuffix(baseName, ".kicad_mod")
		destFootprintPath := filepath.Join(prettyFolder, baseName)

		var fpErr error
		if autoName != "" || finalModelName != "" {
			// Now updates both the internal 3D path AND the internal component name
			fpErr = patchFootprint(assets.FootprintPath, destFootprintPath, category, finalFootprintName, finalModelName, repoName)
			fmt.Println("--> Copied & Patched Footprint to:", destFootprintPath)
		} else {
			fpErr = copyFile(assets.FootprintPath, destFootprintPath)
			fmt.Println("--> Copied Footprint to:", destFootprintPath)
		}
		if fpErr != nil {
			fmt.Println("Warning: failed to write footprint:", fpErr)
		} else {
			addedFiles = append(addedFiles, destFootprintPath)
			UpdateKiCadFpTable(getLibNickname(repoName, category), prettyFolder)
		}
	}

	// 3. Handle Symbols
	if assets.SymbolPath != "" {
		masterSym = filepath.Join(symbolsFolder, fmt.Sprintf("%s.kicad_sym", category))
		backupSym = masterSym + ".bak"

		masterExisted := false
		if _, err := os.Stat(masterSym); err == nil {
			masterExisted = true
			if err := copyFile(masterSym, backupSym); err != nil {
				return addedFiles, "", "", fmt.Errorf("failed to back up symbol library: %w", err)
			}
		}

		if err := injectSymbol(assets.SymbolPath, masterSym, category, finalFootprintName, repoName, conflictStrategy, newName); err != nil {
			if masterExisted {
				os.Rename(backupSym, masterSym)
			} else {
				os.Remove(masterSym)
			}
			return addedFiles, "", "", fmt.Errorf("failed to inject symbol: %w", err)
		}
		fmt.Println("--> Injected & Sanitized Symbol into:", masterSym)
		UpdateKiCadSymTable(getLibNickname(repoName, category), masterSym)

		if !masterExisted {
			addedFiles = append(addedFiles, masterSym)
			masterSym = ""
			backupSym = ""
		}
	}

	// 4. Handle Design Blocks — each block gets its own .kicad_block subfolder
	blockName := autoName
	if blockName == "" {
		src := assets.SchBlockPath
		if src == "" {
			src = assets.PcbBlockPath
		}
		blockName = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}

	if assets.SchBlockPath != "" {
		blockDir := filepath.Join(blocksLibFolder, fmt.Sprintf("%s.kicad_block", blockName))
		if err := os.MkdirAll(blockDir, os.ModePerm); err != nil {
			fmt.Println("Warning: failed to create schematic block dir:", err)
		} else {
			destSch := filepath.Join(blockDir, "design_block.kicad_sch")
			if err := copyFile(assets.SchBlockPath, destSch); err != nil {
				fmt.Println("Warning: failed to copy schematic block:", err)
			} else {
				addedFiles = append(addedFiles, blockDir)
				fmt.Println("--> Copied Schematic Design Block to:", destSch)
				UpdateKiCadBlockTable(getLibNickname(repoName, category), blocksLibFolder)
			}
		}
	}
	if assets.PcbBlockPath != "" {
		blockDir := filepath.Join(blocksLibFolder, fmt.Sprintf("%s.kicad_block", blockName))
		if err := os.MkdirAll(blockDir, os.ModePerm); err != nil {
			fmt.Println("Warning: failed to create PCB block dir:", err)
		} else {
			destPcb := filepath.Join(blockDir, "design_block.kicad_pcb")
			if err := copyFile(assets.PcbBlockPath, destPcb); err != nil {
				fmt.Println("Warning: failed to copy PCB block:", err)
			} else {
				addedFiles = append(addedFiles, blockDir)
				fmt.Println("--> Copied PCB Design Block to:", destPcb)
				UpdateKiCadBlockTable(getLibNickname(repoName, category), blocksLibFolder)
			}
		}
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

	UpdateKiCadEnvVar(conf.BaseLibPath)

	for _, repo := range conf.Repositories {
		targetRepoRoot := filepath.Join(conf.BaseLibPath, repo.Name)
		for _, category := range conf.Categories {
			nickname := getLibNickname(repo.Name, category)

			// Setup Footprint Library Folder
			prettyPath := filepath.Join(targetRepoRoot, "footprints", fmt.Sprintf("%s.pretty", category))
			os.MkdirAll(prettyPath, os.ModePerm)
			UpdateKiCadFpTable(nickname, prettyPath)

			// Setup Symbol Library File
			symDir := filepath.Join(targetRepoRoot, "symbols")
			symPath := filepath.Join(symDir, fmt.Sprintf("%s.kicad_sym", category))

			if _, err := os.Stat(symPath); os.IsNotExist(err) {
				os.MkdirAll(symDir, os.ModePerm)
				emptyLib := "(kicad_symbol_lib (version 20211014) (generator kicad_symbol_editor)\n)\n"
				if writeErr := os.WriteFile(symPath, []byte(emptyLib), 0644); writeErr != nil {
					fmt.Printf("Warning: failed to create symbol library %s: %v\n", symPath, writeErr)
				}
			}
			UpdateKiCadSymTable(nickname, symPath)
		}
	}
}

func injectSymbol(sourceFile, masterFile, category, footprintName, repoName string, conflictStrategy string, newName string) error {
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

	// Handle internal KiCad S-Expression Renaming safely
	if conflictStrategy == "rename" && newName != "" {
		reNameExtract := regexp.MustCompile(`\(\s*symbol\s+"([^"]+)"`)
		nameMatch := reNameExtract.FindStringSubmatch(srcContent)
		if len(nameMatch) > 1 {
			oldName := nameMatch[1]
			// Replace the exact quoted name matching the old symbol
			extractedSymbol = strings.ReplaceAll(extractedSymbol, `"`+oldName+`"`, `"`+newName+`"`)
			// Safely handle KiCad's internal sub-symbol linking syntax (e.g. "OldName_0_1")
			extractedSymbol = strings.ReplaceAll(extractedSymbol, `"`+oldName+`_`, `"`+newName+`_`)
		}
	}

	if footprintName != "" {
		reFootprintProp := regexp.MustCompile(`\(property\s+"Footprint"\s+"[^"]*"`)
		// Dynamically fetch the correct nickname for the footprint property mapping
		newProp := fmt.Sprintf(`(property "Footprint" "%s:%s"`, getLibNickname(repoName, category), footprintName)
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

func UpdateKiCadEnvVar(basePath string) error {
	kicadBase := filepath.Join(kicadConfigDir(), "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || !kicadVersionRegex.MatchString(entry.Name()) {
			continue
		}

		commonJsonPath := filepath.Join(kicadBase, entry.Name(), "kicad_common.json")

		var configData map[string]interface{}
		fileBytes, err := os.ReadFile(commonJsonPath)
		if err == nil {
			if jsonErr := json.Unmarshal(fileBytes, &configData); jsonErr != nil {
				fmt.Printf("Warning: malformed kicad_common.json for KiCad %s: %v\n", entry.Name(), jsonErr)
			}
		}

		if configData == nil {
			configData = make(map[string]interface{})
		}

		// 1. Safely handle the "environment" section
		env, ok := configData["environment"].(map[string]interface{})
		if !ok || env == nil {
			env = make(map[string]interface{})
			configData["environment"] = env
		}

		// 2. Safely handle the "vars" section
		vars, ok := env["vars"].(map[string]interface{})
		if !ok || vars == nil {
			vars = make(map[string]interface{})
			env["vars"] = vars
		}

		// 3. Update the variable
		vars["KICAD_USER_3DMODEL_DIR"] = basePath

		newJson, err := json.MarshalIndent(configData, "", "  ")
		if err != nil {
			continue
		}

		err = os.WriteFile(commonJsonPath, newJson, 0644)
		if err == nil {
			fmt.Printf("--> Registered KICAD_USER_3DMODEL_DIR in KiCad %s\n", entry.Name())
		}
	}
	return nil
}

func patchFootprint(src, dest, category, newFpName, modelFileName, repoName string) error {
	contentBytes, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	// 1. Patch the internal footprint name so KiCad doesn't complain about mismatches
	if newFpName != "" {
		// Targets the very first line: e.g. (footprint "OldMessyName" or (module "OldMessyName"
		reFpName := regexp.MustCompile(`(?i)^(\s*\(\s*(?:footprint|module)\s+)"[^"]+"`)
		content = reFpName.ReplaceAllString(content, `${1}"`+newFpName+`"`)
	}

	// 2. Patch the 3D model path
	if modelFileName != "" {
		re := regexp.MustCompile(`(?i)(\(model\s+)"?([^"\)]+\.(?:step|stp|wrl))"?`)

		if re.MatchString(content) {
			newPathStr := fmt.Sprintf(`${1}"$${KICAD_USER_3DMODEL_DIR}/%s/packages3d/%s.3dshapes/%s"`, repoName, category, modelFileName)
			content = re.ReplaceAllString(content, newPathStr)
		} else {
			newModelPath := fmt.Sprintf("(model \"${KICAD_USER_3DMODEL_DIR}/%s/packages3d/%s.3dshapes/%s\"\n    (offset (xyz 0 0 0)) (scale (xyz 1 1 1)) (rotate (xyz 0 0 0))\n  )", repoName, category, modelFileName)
			lastParenIdx := strings.LastIndex(content, ")")
			if lastParenIdx != -1 {
				content = content[:lastParenIdx] + "  " + newModelPath + "\n)"
			}
		}
	}

	return os.WriteFile(dest, []byte(content), 0644)
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
	kicadBase := filepath.Join(kicadConfigDir(), "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || !kicadVersionRegex.MatchString(entry.Name()) {
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
		if err := os.WriteFile(tablePath, []byte(newContent), 0644); err != nil {
			fmt.Printf("Warning: failed to write sym-lib-table for KiCad %s: %v\n", entry.Name(), err)
			continue
		}
		fmt.Printf("--> Registered symbol library %s in KiCad %s\n", libNickname, entry.Name())
	}
	return nil
}

func UpdateKiCadFpTable(libNickname, libPath string) error {
	kicadBase := filepath.Join(kicadConfigDir(), "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || !kicadVersionRegex.MatchString(entry.Name()) {
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
		if err := os.WriteFile(tablePath, []byte(newContent), 0644); err != nil {
			fmt.Printf("Warning: failed to write fp-lib-table for KiCad %s: %v\n", entry.Name(), err)
			continue
		}
		fmt.Printf("--> Registered footprint library %s in KiCad %s\n", libNickname, entry.Name())
	}
	return nil
}

func UpdateKiCadBlockTable(libNickname, libPath string) error {
	kicadBase := filepath.Join(kicadConfigDir(), "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || !kicadVersionRegex.MatchString(entry.Name()) {
			continue
		}

		tablePath := filepath.Join(kicadBase, entry.Name(), "design_block_lib_table")

		content, err := os.ReadFile(tablePath)
		if err != nil {
			content = []byte("(design_block_lib_table\n)")
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
		if err := os.WriteFile(tablePath, []byte(newContent), 0644); err != nil {
			fmt.Printf("Warning: failed to write design_block_lib_table for KiCad %s: %v\n", entry.Name(), err)
			continue
		}
		fmt.Printf("--> Registered design block library %s in KiCad %s\n", libNickname, entry.Name())
	}
	return nil
}

// getLibNickname intelligently determines the KiCad library table nickname based on if the repo is the primary one.
func getLibNickname(repoName, category string) string {
	conf := LoadConfig()

	// If this repo is the designated default, or if it's the very first/only repo
	isPrimary := repoName == conf.DefaultRepo ||
		(conf.DefaultRepo == "" && len(conf.Repositories) > 0 && repoName == conf.Repositories[0].Name)

	if isPrimary {
		return category // Clean name: e.g., "Connectors"
	}
	return fmt.Sprintf("%s_%s", repoName, category) // Safe name: e.g., "Github_Connectors"
}
