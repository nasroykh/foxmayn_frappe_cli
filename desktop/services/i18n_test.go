package services

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestSetLanguage(t *testing.T) {
	t.Cleanup(func() { uiLanguage.Store(LangEnglish) })
	s := &AppService{}
	for in, want := range map[string]string{
		"fr": "fr", "fr-FR": "fr", "AR": "ar", "ar_DZ": "ar", "en-US": "en", "de": "en", "": "en", " fr ": "fr",
	} {
		if got := s.SetLanguage(in); got != want || currentLanguage() != want {
			t.Errorf("SetLanguage(%q) = %q, current %q; want %q", in, got, currentLanguage(), want)
		}
	}
	s.SetLanguage("ar")
	if got := dialogT("dropCancel"); got != "إلغاء" {
		t.Errorf("dialogT in Arabic = %q", got)
	}
	s.SetLanguage("de")
	if got := dialogT("dropCancel"); got != "Cancel" {
		t.Errorf("dialogT fallback = %q", got)
	}
}

var verbs = regexp.MustCompile(`%[sdvq]`)

func TestDialogStrings(t *testing.T) {
	for key, m := range dialogStrings {
		en := verbs.FindAllString(m[LangEnglish], -1)
		for _, l := range []string{LangEnglish, LangFrench, LangArabic} {
			if strings.TrimSpace(m[l]) == "" {
				t.Errorf("%s: no %s text", key, l)
				continue
			}
			if got := verbs.FindAllString(m[l], -1); strings.Join(got, ",") != strings.Join(en, ",") {
				t.Errorf("%s (%s): verbs %v, English has %v", key, l, got, en)
			}
		}
	}
}

// catalogString reads errors.<key> (dots are nesting) from a locale file.
func catalogString(t *testing.T, cat map[string]any, key string) (string, bool) {
	t.Helper()
	var cur any = cat["errors"]
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur = m[part]
	}
	s, ok := cur.(string)
	return s, ok
}

func TestErrorKeysMatchCatalog(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "frontend", "src", "i18n", "locales", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cat map[string]any
	if err := json.Unmarshal(data, &cat); err != nil {
		t.Fatal(err)
	}
	for key, text := range errorKeys {
		got, ok := catalogString(t, cat, key)
		if !ok {
			t.Errorf("en.json has no errors.%s", key)
		} else if got != text {
			t.Errorf("errors.%s: en.json %q, Go %q", key, got, text)
		}
	}
}

// keyFuncs are the functions that take an errorKeys key, by argument index.
var keyFuncs = map[string]int{"keyed": 1, "keyedInvalid": 1, "setKeyed": 1}

// TestErrorKeysUsed checks the keys the services pass to keyed,
// keyedInvalid, setKeyed and siteError against errorKeys, both ways: none
// unknown, none unused. Keys are literals, except siteError's own, built from
// the step constant each siteError call names, and those the key functions
// pass on.
func TestErrorKeysUsed(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	used := map[string]string{}
	steps := map[string]string{} // constant name -> value
	var stepCalls []*ast.Ident
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			// siteError and the key functions themselves pass a key on.
			inSiteError, inKeyFunc := false, false
			if fd, ok := decl.(*ast.FuncDecl); ok {
				_, inKeyFunc = keyFuncs[fd.Name.Name]
				inSiteError = fd.Recv == nil && fd.Name.Name == "siteError"
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.ValueSpec:
					for i, name := range n.Names {
						if strings.HasPrefix(name.Name, "step") && i < len(n.Values) {
							if lit, ok := n.Values[i].(*ast.BasicLit); ok {
								v, _ := strconv.Unquote(lit.Value)
								steps[name.Name] = v
							}
						}
					}
				case *ast.CallExpr:
					var fn string
					switch f := n.Fun.(type) {
					case *ast.Ident:
						fn = f.Name
					case *ast.SelectorExpr:
						fn = f.Sel.Name
					}
					pos := fset.Position(n.Pos()).String()
					if fn == "siteError" {
						id, ok := n.Args[0].(*ast.Ident)
						if !ok {
							t.Errorf("%s: siteError without a step constant", pos)
						} else if !inSiteError {
							stepCalls = append(stepCalls, id)
						}
						return true
					}
					idx, ok := keyFuncs[fn]
					if !ok || len(n.Args) <= idx {
						return true
					}
					lit, ok := n.Args[idx].(*ast.BasicLit)
					if !ok {
						if !inSiteError && !inKeyFunc {
							t.Errorf("%s: %s with a computed key", pos, fn)
						}
						return true
					}
					v, _ := strconv.Unquote(lit.Value)
					used[v] = pos
				}
				return true
			})
		}
	}
	if len(stepCalls) == 0 {
		t.Fatal("no siteError calls found")
	}
	for _, id := range stepCalls {
		v, ok := steps[id.Name]
		if !ok {
			t.Errorf("%s: siteError(%s): not a step constant", fset.Position(id.Pos()), id.Name)
			continue
		}
		used["site."+v+"Cancelled"] = "siteError"
		used["site."+v+"Failed"] = "siteError"
	}
	var bad []string
	for k, pos := range used {
		if _, ok := errorKeys[k]; !ok {
			bad = append(bad, "unknown "+k+" ("+pos+")")
		}
	}
	for k := range errorKeys {
		if _, ok := used[k]; !ok {
			bad = append(bad, "unused "+k)
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
}

func TestKeyedError(t *testing.T) {
	e := keyed(CodeFailed, "site.status", nil, "status", "502")
	if e.Message != "The site answered with an error (502)." || e.Key != "site.status" || e.Args["status"] != "502" {
		t.Errorf("keyed = %+v", e)
	}
	b, _ := json.Marshal(e)
	if !strings.Contains(string(b), `"key":"site.status","args":{"status":"502"}`) {
		t.Errorf("json = %s", b)
	}
	if e := keyedInvalid("url", "site.refused"); e.Code != CodeInvalid || e.Field != "url" || e.Args != nil {
		t.Errorf("keyedInvalid = %+v", e)
	}
}
