package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ComponentEntry is a single searchable item (a symbol or a design block) found
// by scanning the registered library repositories on disk.
type ComponentEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // "Symbol" | "Design Block"
	Repo        string `json:"repo"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Keywords    string `json:"keywords"`
	Reference   string `json:"reference"` // KiCad library reference: nickname:name
}

var (
	reSymbolName  = regexp.MustCompile(`\(\s*symbol\s+"([^"]+)"`)
	rePropFactory = func(prop string) *regexp.Regexp {
		return regexp.MustCompile(`(?s)\(\s*property\s+"` + regexp.QuoteMeta(prop) + `"\s+"([^"]*)"`)
	}
	reDescription = rePropFactory("ki_description")
	reDescAlt     = rePropFactory("Description")
	reKeywords    = rePropFactory("ki_keywords")
	reKeywordsAlt = rePropFactory("Keywords")
)

// SearchIndex scans every registered repository's symbol libraries and design
// block folders and returns a flat, searchable index. It reads the filesystem
// (not the import history), so it also surfaces parts from cloned/pre-existing
// libraries. The frontend filters this list locally as the user types.
func (a *App) SearchIndex() []ComponentEntry {
	conf := LoadConfig()
	out := []ComponentEntry{}
	if conf.BaseLibPath == "" {
		return out
	}

	for _, repo := range conf.Repositories {
		repoRoot := filepath.Join(conf.BaseLibPath, repo.Name)
		out = append(out, indexSymbols(repo.Name, repoRoot)...)
		out = append(out, indexDesignBlocks(repo.Name, repoRoot)...)
	}
	return out
}

// CategoryCounts returns the number of indexed items (symbols + design blocks)
// per category, summed across all repositories. Categories with no items are
// simply absent from the map (the frontend defaults those to 0).
func (a *App) CategoryCounts() map[string]int {
	counts := map[string]int{}
	for _, e := range a.SearchIndex() {
		counts[e.Category]++
	}
	return counts
}

func indexSymbols(repoName, repoRoot string) []ComponentEntry {
	var out []ComponentEntry
	symDir := filepath.Join(repoRoot, "symbols")
	entries, err := os.ReadDir(symDir)
	if err != nil {
		return out
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".kicad_sym") {
			continue
		}
		category := strings.TrimSuffix(e.Name(), ".kicad_sym")
		data, err := os.ReadFile(filepath.Join(symDir, e.Name()))
		if err != nil {
			continue
		}
		nickname := getLibNickname(repoName, category)
		for _, sym := range parseTopLevelSymbols(string(data)) {
			out = append(out, ComponentEntry{
				Name:        sym.name,
				Type:        "Symbol",
				Repo:        repoName,
				Category:    category,
				Description: sym.description,
				Keywords:    sym.keywords,
				Reference:   nickname + ":" + sym.name,
			})
		}
	}
	return out
}

func indexDesignBlocks(repoName, repoRoot string) []ComponentEntry {
	var out []ComponentEntry
	blocksRoot := filepath.Join(repoRoot, "blocks")
	catDirs, err := os.ReadDir(blocksRoot)
	if err != nil {
		return out
	}

	for _, cd := range catDirs {
		if !cd.IsDir() || !strings.HasSuffix(cd.Name(), ".kicad_blocks") {
			continue
		}
		category := strings.TrimSuffix(cd.Name(), ".kicad_blocks")
		libDir := filepath.Join(blocksRoot, cd.Name())
		blockDirs, err := os.ReadDir(libDir)
		if err != nil {
			continue
		}
		nickname := getLibNickname(repoName, category)
		for _, bd := range blockDirs {
			if !bd.IsDir() || !strings.HasSuffix(bd.Name(), ".kicad_block") {
				continue
			}
			name := strings.TrimSuffix(bd.Name(), ".kicad_block")

			var desc, kw string
			metaPath := filepath.Join(libDir, bd.Name(), name+".json")
			if mb, err := os.ReadFile(metaPath); err == nil {
				var m designBlockMeta
				if json.Unmarshal(mb, &m) == nil {
					desc, kw = m.Description, m.Keywords
				}
			}

			out = append(out, ComponentEntry{
				Name:        name,
				Type:        "Design Block",
				Repo:        repoName,
				Category:    category,
				Description: desc,
				Keywords:    kw,
				Reference:   nickname + ":" + name,
			})
		}
	}
	return out
}

type parsedSymbol struct {
	name        string
	description string
	keywords    string
}

// parseTopLevelSymbols extracts the top-level (symbol "...") blocks from a
// kicad_symbol_lib s-expression, skipping the nested unit sub-symbols (e.g.
// "Name_0_1") so each component appears once.
func parseTopLevelSymbols(content string) []parsedSymbol {
	var out []parsedSymbol
	depth := 0
	inStr := false

	for i := 0; i < len(content); {
		c := content[i]
		if inStr {
			if c == '\\' {
				i += 2
				continue
			}
			if c == '"' {
				inStr = false
			}
			i++
			continue
		}

		switch c {
		case '"':
			inStr = true
			i++
		case '(':
			// depth == 1 means we are directly inside the (kicad_symbol_lib ...)
			// wrapper, i.e. this opens a top-level component symbol.
			if depth == 1 && tokenAfterParenIs(content, i, "symbol") {
				end := matchingParen(content, i)
				if end > i {
					block := content[i : end+1]
					if name := firstMatch(reSymbolName, block); name != "" {
						out = append(out, parsedSymbol{
							name:        name,
							description: firstMatchAny(block, reDescription, reDescAlt),
							keywords:    firstMatchAny(block, reKeywords, reKeywordsAlt),
						})
					}
					i = end + 1 // skip the whole block; depth is unchanged (balanced)
					continue
				}
			}
			depth++
			i++
		case ')':
			depth--
			i++
		default:
			i++
		}
	}
	return out
}

// tokenAfterParenIs reports whether the first token after the '(' at openIdx is
// exactly tok (followed by whitespace or a quote).
func tokenAfterParenIs(s string, openIdx int, tok string) bool {
	j := openIdx + 1
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
		j++
	}
	if j+len(tok) > len(s) || s[j:j+len(tok)] != tok {
		return false
	}
	k := j + len(tok)
	return k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\n' || s[k] == '\r' || s[k] == '"')
}

// matchingParen returns the index of the ')' that closes the '(' at openIdx,
// honoring quoted strings, or -1 if unbalanced.
func matchingParen(s string, openIdx int) int {
	depth := 0
	inStr := false
	for i := openIdx; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
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
				return i
			}
		}
	}
	return -1
}

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func firstMatchAny(s string, res ...*regexp.Regexp) string {
	for _, re := range res {
		if v := firstMatch(re, s); v != "" {
			return v
		}
	}
	return ""
}
