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

// IntegrateParts moves extracted assets and returns tracking info for Undo functionality.
// blockDescription/blockKeywords populate the design block's metadata .json (ignored
// when no design block is present).
func IntegrateParts(assets *KiCadAssets, category string, targetRepoRoot string, repoName string, conflictStrategy string, newName string, blockDescription string, blockKeywords string) ([]string, string, string, error) {

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
		// Store the backup OUTSIDE the library repo so `git add .` never stages
		// and pushes it to the shared remote. It lives in the app's data dir and
		// is referenced by absolute path from the undo history.
		backupSym = symbolBackupPath(repoName, category)

		masterExisted := false
		if _, err := os.Stat(masterSym); err == nil {
			masterExisted = true
			if err := os.MkdirAll(filepath.Dir(backupSym), os.ModePerm); err != nil {
				return addedFiles, "", "", fmt.Errorf("failed to create backup directory: %w", err)
			}
			if err := copyFile(masterSym, backupSym); err != nil {
				return addedFiles, "", "", fmt.Errorf("failed to back up symbol library: %w", err)
			}
		}

		if err := injectSymbol(assets.SymbolPath, masterSym, category, finalFootprintName, repoName, conflictStrategy, newName); err != nil {
			if masterExisted {
				// Rename can fail across volumes (backup lives in the app data dir,
				// the library may be on another drive), so fall back to a copy.
				if renErr := os.Rename(backupSym, masterSym); renErr != nil {
					copyFile(backupSym, masterSym)
				}
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

	// 4. Handle Design Blocks — each block gets its own .kicad_block subfolder.
	// KiCad locates the schematic/board/metadata inside the folder by the design
	// block name, so every file must share the block's base name (not a fixed
	// "design_block.*" name and not the symbol's auto-detected name).
	if assets.SchBlockPath != "" || assets.PcbBlockPath != "" {
		blockSrc := assets.SchBlockPath
		if blockSrc == "" {
			blockSrc = assets.PcbBlockPath
		}

		blockName := strings.TrimSuffix(filepath.Base(blockSrc), filepath.Ext(blockSrc))
		// A manual rename from the UI overrides the source-derived name.
		if conflictStrategy == "rename" && newName != "" {
			blockName = newName
		}

		blockDir := filepath.Join(blocksLibFolder, fmt.Sprintf("%s.kicad_block", blockName))
		if err := os.MkdirAll(blockDir, os.ModePerm); err != nil {
			fmt.Println("Warning: failed to create design block dir:", err)
		} else {
			blockOK := false

			if assets.SchBlockPath != "" {
				destSch := filepath.Join(blockDir, blockName+".kicad_sch")
				if err := copyFile(assets.SchBlockPath, destSch); err != nil {
					fmt.Println("Warning: failed to copy schematic block:", err)
				} else {
					blockOK = true
					fmt.Println("--> Copied Schematic Design Block to:", destSch)
				}
			}

			if assets.PcbBlockPath != "" {
				destPcb := filepath.Join(blockDir, blockName+".kicad_pcb")
				if err := copyFile(assets.PcbBlockPath, destPcb); err != nil {
					fmt.Println("Warning: failed to copy PCB block:", err)
				} else {
					blockOK = true
					fmt.Println("--> Copied PCB Design Block to:", destPcb)
				}
			}

			if blockOK {
				// KiCad expects a <blockName>.json metadata file alongside the
				// block files. Write the user-supplied description/keywords; if both
				// are empty only seed a minimal file when none exists yet (so an
				// existing file's metadata is preserved on overwrite).
				metaPath := filepath.Join(blockDir, blockName+".json")
				_, statErr := os.Stat(metaPath)
				if blockDescription != "" || blockKeywords != "" || os.IsNotExist(statErr) {
					if err := writeDesignBlockMeta(metaPath, blockDescription, blockKeywords); err != nil {
						fmt.Println("Warning: failed to write design block metadata:", err)
					}
				}
				// Track the directory once (used by Undo via os.RemoveAll).
				addedFiles = append(addedFiles, blockDir)
				UpdateKiCadBlockTable(getLibNickname(repoName, category), blocksLibFolder)
			}
		}
	}

	return addedFiles, masterSym, backupSym, nil
}

// designBlockMeta mirrors KiCad's <name>.json design block metadata. Field order
// matches what KiCad writes (description, keywords, then fields).
type designBlockMeta struct {
	Description string            `json:"description"`
	Keywords    string            `json:"keywords"`
	Fields      map[string]string `json:"fields"`
}

// writeDesignBlockMeta writes a design block's metadata json, preserving any
// existing custom "fields" map if the file already exists.
func writeDesignBlockMeta(path, description, keywords string) error {
	meta := designBlockMeta{Description: description, Keywords: keywords, Fields: map[string]string{}}

	// Preserve an existing fields map (and fall back to existing text when the
	// caller passed nothing) so overwrites don't wipe user-entered metadata.
	if existing, err := os.ReadFile(path); err == nil {
		var prev designBlockMeta
		if json.Unmarshal(existing, &prev) == nil {
			if prev.Fields != nil {
				meta.Fields = prev.Fields
			}
			if description == "" {
				meta.Description = prev.Description
			}
			if keywords == "" {
				meta.Keywords = prev.Keywords
			}
		}
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
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

			// Setup Design Block Library Folder (an empty .kicad_blocks folder is
			// a valid, empty design block library). Registering it here means
			// categories appear in KiCad's design block browser immediately and
			// existing categories get migrated to the correct table file.
			blocksPath := filepath.Join(targetRepoRoot, "blocks", fmt.Sprintf("%s.kicad_blocks", category))
			os.MkdirAll(blocksPath, os.ModePerm)
			UpdateKiCadBlockTable(nickname, blocksPath)
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

	// Determine the symbol's final name (used below to replace any existing
	// definition of the same name instead of appending a duplicate).
	reNameExtract := regexp.MustCompile(`\(\s*symbol\s+"([^"]+)"`)
	nameMatch := reNameExtract.FindStringSubmatch(srcContent)
	var finalName string
	if len(nameMatch) > 1 {
		finalName = nameMatch[1]
	}

	// Handle internal KiCad S-Expression Renaming safely
	if conflictStrategy == "rename" && newName != "" {
		if finalName != "" {
			oldName := finalName
			// Replace the exact quoted name matching the old symbol
			extractedSymbol = strings.ReplaceAll(extractedSymbol, `"`+oldName+`"`, `"`+newName+`"`)
			// Safely handle KiCad's internal sub-symbol linking syntax (e.g. "OldName_0_1")
			extractedSymbol = strings.ReplaceAll(extractedSymbol, `"`+oldName+`_`, `"`+newName+`_`)
		}
		finalName = newName
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

	// If a symbol with this name already exists (e.g. re-import or "overwrite"
	// conflict strategy), remove the old definition so we replace it instead of
	// appending a duplicate (KiCad rejects libraries with duplicate symbol names).
	if finalName != "" {
		masterContent = removeSymbolByName(masterContent, finalName)
	}

	masterLastParenIdx := strings.LastIndex(masterContent, ")")
	if masterLastParenIdx == -1 {
		return fmt.Errorf("master symbol file is malformed")
	}

	newMasterContent := masterContent[:masterLastParenIdx] + "\n  " + extractedSymbol + "\n)\n"
	return os.WriteFile(masterFile, []byte(newMasterContent), 0644)
}

// removeSymbolByName removes a top-level (symbol "name" ...) block from a
// kicad_symbol_lib s-expression. It walks the matching parentheses (ignoring
// parens inside quoted strings) to delete the whole balanced block. The content
// is returned unchanged if the symbol is not found or the block is unbalanced.
func removeSymbolByName(content, name string) string {
	re := regexp.MustCompile(`\(\s*symbol\s+"` + regexp.QuoteMeta(name) + `"`)
	loc := re.FindStringIndex(content)
	if loc == nil {
		return content
	}
	start := loc[0]

	depth := 0
	inStr := false
	for i := start; i < len(content); i++ {
		c := content[i]
		if inStr {
			switch c {
			case '\\':
				i++ // skip the escaped character
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				// Drop the block plus any leading whitespace/newline so repeated
				// overwrites don't accumulate blank lines.
				left := strings.TrimRight(content[:start], " \t\r\n")
				return left + "\n" + content[i+1:]
			}
		}
	}
	return content // unbalanced — leave untouched rather than corrupt the file
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

		var configData map[string]any
		fileBytes, err := os.ReadFile(commonJsonPath)
		if err == nil {
			if jsonErr := json.Unmarshal(fileBytes, &configData); jsonErr != nil {
				fmt.Printf("Warning: malformed kicad_common.json for KiCad %s: %v\n", entry.Name(), jsonErr)
			}
		}

		if configData == nil {
			configData = make(map[string]any)
		}

		// 1. Safely handle the "environment" section
		env, ok := configData["environment"].(map[string]any)
		if !ok || env == nil {
			env = make(map[string]any)
			configData["environment"] = env
		}

		// 2. Safely handle the "vars" section
		vars, ok := env["vars"].(map[string]any)
		if !ok || vars == nil {
			vars = make(map[string]any)
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

		// NOTE: the file name uses hyphens (like sym-lib-table / fp-lib-table)
		// even though the root s-expression token uses underscores. KiCad only
		// reads the hyphenated file; writing to "design_block_lib_table" leaves
		// the registrations in a file KiCad ignores.
		tablePath := filepath.Join(kicadBase, entry.Name(), "design-block-lib-table")

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
