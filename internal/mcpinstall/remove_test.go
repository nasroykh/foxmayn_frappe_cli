package mcpinstall

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tailscale/hujson"
)

func mustPlanRemove(t *testing.T, client, name string, env Env) *Change {
	t.Helper()
	c, err := PlanRemove(client, name, env)
	if err != nil {
		t.Fatalf("PlanRemove(%s): %v", client, err)
	}
	return c
}

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

func TestRemoveJSON(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"middle, comments on its lines go, comment lines stay": {
			in: `{
  // my servers
  "mcpServers": {
    "a": {"command": "a"},
    // ffc, added by hand
    /* same line */ "frappe": {
      "command": "ffc", // the binary
      "args": ["mcp"]
    }, // trailing note
    "b": {"command": "b"}
  },
  "other": 1
}
`,
			want: `{
  // my servers
  "mcpServers": {
    "a": {"command": "a"},
    // ffc, added by hand
    "b": {"command": "b"}
  },
  "other": 1
}
`,
		},
		"first, comment in between and after": {
			in: `{
  "mcpServers": { // servers
    "frappe": /* in between */ {"command": "ffc"}, // ffc
    "a": {"command": "a"}
  }
}
`,
			want: `{
  "mcpServers": { // servers
    "a": {"command": "a"}
  }
}
`,
		},
		"last, the comma goes and the comment before stays": {
			in: `{
  "mcpServers": {
    "a": {"command": "a"}, // keep me
    "frappe": {"command": "ffc"} // ffc
  }
}
`,
			want: `{
  "mcpServers": {
    "a": {"command": "a"} // keep me
  }
}
`,
		},
		"last, trailing comma style kept": {
			in: `{
	"mcpServers": {
		"a": {"command": "a"},
		"frappe": {"command": "ffc"},
	},
}
`,
			want: `{
	"mcpServers": {
		"a": {"command": "a"},
	},
}
`,
		},
		"last, comment lines before the closing brace stay": {
			in: `{
  "mcpServers": {
    "a": {},
    "frappe": {}
    // "old": {}
  }
}`,
			want: `{
  "mcpServers": {
    "a": {}
    // "old": {}
  }
}`,
		},
		"only member": {
			in: `{
  "mcpServers": {
    "frappe": {"command": "ffc", "args": []}
  },
  "globalShortcut": "x"
}
`,
			want: `{
  "mcpServers": {},
  "globalShortcut": "x"
}
`,
		},
		"only member, comment line kept": {
			in: `{
  "mcpServers": {
    // nothing else
    "frappe": {"command": "ffc"},
  }
}
`,
			want: `{
  "mcpServers": {
    // nothing else
  }
}
`,
		},
		"one line, middle": {
			in:   `{"mcpServers": {"a": 1, "frappe": {"command": "ffc"}, "b": 2}}`,
			want: `{"mcpServers": {"a": 1, "b": 2}}`,
		},
		"one line, first": {
			in:   `{"mcpServers": {"frappe": {}, "b": 2}}`,
			want: `{"mcpServers": {"b": 2}}`,
		},
		"one line, last": {
			in:   `{"mcpServers": {"a": 1, "frappe": {} }}`,
			want: `{"mcpServers": {"a": 1 }}`,
		},
		"one line, only": {
			in:   `{"mcpServers": {"frappe": {}}}`,
			want: `{"mcpServers": {}}`,
		},
		"a block comment spanning lines after the comma stays": {
			in: `{
  "mcpServers": {
    "frappe": {"command": "a"}, /* note about
       other */
    "other": {"command": "b"}
  }
}
`,
			want: `{
  "mcpServers": {
    /* note about
       other */
    "other": {"command": "b"}
  }
}
`,
		},
		"a block comment spanning lines before the name stays": {
			in: `{
  "mcpServers": {
    "a": 1,
    /* about
       a */ "frappe": 2,
    "b": 3
  }
}
`,
			want: `{
  "mcpServers": {
    "a": 1,
    /* about
       a */
    "b": 3
  }
}
`,
		},
		"one line, the next member keeps its comment": {
			in:   `{"mcpServers": {"frappe": {"command": "a"}, /* keep: other */ "other": {"command": "b"}}}`,
			want: `{"mcpServers": {/* keep: other */ "other": {"command": "b"}}}`,
		},
		"comma-first, middle": {
			in: `{
  "mcpServers": {
    "a": 1
    , "frappe": 2
    // about b
    , "b": 3
  }
}
`,
			want: `{
  "mcpServers": {
    "a": 1
    // about b
    , "b": 3
  }
}
`,
		},
		"comma-first, last": {
			in: `{
  "mcpServers": {
    "a": 1
    , "frappe": 2
  }
}
`,
			want: `{
  "mcpServers": {
    "a": 1
  }
}
`,
		},
		"comma-first, first": {
			in: `{
  "mcpServers": {
    "frappe": 1 // own
    // about b
    , "b": 2
  }
}
`,
			want: `{
  "mcpServers": {
    // about b
    "b": 2
  }
}
`,
		},
		"next member on the same line takes its place": {
			in: `{
  "mcpServers": {
    "a": 1,
    "frappe": {}, "b": 2
  }
}`,
			want: `{
  "mcpServers": {
    "a": 1,
    "b": 2
  }
}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, conv := range []func(string) string{func(s string) string { return s }, crlf} {
				in, want := conv(tc.in), conv(tc.want)
				out, found, err := removeJSON([]byte(in), "mcpServers", "frappe")
				if err != nil || !found {
					t.Fatalf("found %v, err %v", found, err)
				}
				if string(out) != want {
					t.Fatalf("got:\n%q\nwant:\n%q", out, want)
				}
			}
		})
	}
}

// Every member of every layout can be removed: the result check passes and
// the other members stay.
func TestRemoveJSONEveryMember(t *testing.T) {
	layouts := []string{
		"{\"mcpServers\": {\"a\": 1, \"b\": {\"x\": [1, 2]}, \"c\": \"s\"}}",
		"{\n  \"mcpServers\": {\n    \"a\": 1, // a\n    /* b */ \"b\": {\n      \"x\": 2 // in b\n    },\n    // c\n    \"c\": 3\n  }\n}\n",
		"{\n\t\"mcpServers\": {\n\t\t\"a\": 1,\n\t\t\"b\": 2,\n\t\t\"c\": 3,\n\t},\n}\n",
		"{\n  \"mcpServers\": {\n    \"a\": 1\n    , \"b\": 2 // b\n    , \"c\": 3\n  }\n}\n",
		"{\"mcpServers\": {\n  \"a\": 1, \"b\": 2,\n  \"c\": 3}}",
		"{\"mcpServers\": { /* x\n */ \"a\": 1, /* y\n */ \"b\": 2 /* z\n */, \"c\": 3 /* w\n */ }}",
	}
	for _, layout := range layouts {
		for _, conv := range []func(string) string{func(s string) string { return s }, crlf} {
			in := conv(layout)
			for _, name := range []string{"a", "b", "c"} {
				out, found, err := removeJSON([]byte(in), "mcpServers", name)
				if err != nil || !found {
					t.Errorf("%q without %s: %v", in, name, err)
					continue
				}
				for _, other := range []string{"a", "b", "c"} {
					v, _ := hujson.Standardize(append([]byte{}, out...))
					var doc map[string]map[string]interface{}
					if err := json.Unmarshal(v, &doc); err != nil {
						t.Fatalf("%q: %v", out, err)
					}
					if _, ok := doc["mcpServers"][other]; ok == (other == name) {
						t.Errorf("%q without %s: %s present %v", in, name, other, ok)
					}
				}
			}
		}
	}
}

func TestRemoveJSONKeepsStrictAndBOM(t *testing.T) {
	in := "\xEF\xBB\xBF{\"mcpServers\": {\"a\": {}, \"frappe\": {}}}\n"
	out, found, err := removeJSON([]byte(in), "mcpServers", "frappe")
	if err != nil || !found || string(out) != "\xEF\xBB\xBF{\"mcpServers\": {\"a\": {}}}\n" {
		t.Fatalf("%q %v %v", out, found, err)
	}
	v, err := hujson.Parse(out[3:])
	if err != nil || !v.IsStandard() {
		t.Fatalf("not strict JSON: %v", err)
	}

	// Comma-first, last member, CRLF and BOM: no line of blanks is left.
	in = "\xEF\xBB\xBF{\r\n  \"mcpServers\": {\r\n    \"a\": 1\r\n    , \"frappe\": 2\r\n  }\r\n}\r\n"
	out, found, err = removeJSON([]byte(in), "mcpServers", "frappe")
	if want := "\xEF\xBB\xBF{\r\n  \"mcpServers\": {\r\n    \"a\": 1\r\n  }\r\n}\r\n"; err != nil || !found || string(out) != want {
		t.Fatalf("%q %v %v", out, found, err)
	}
}

func TestRemoveJSONAbsent(t *testing.T) {
	for _, in := range []string{
		"",
		" \n",
		"{}",
		`{"mcpServers": {}}`,
		"{\n  // c\n  \"mcpServers\": {\"a\": {}}\n}\n",
		`{"servers": {"frappe": {}}}`, // another client's key
		`{"mcpServers": null}`,
		"{\n  \"mcpServers\": null // none yet\n}\n",
	} {
		out, found, err := removeJSON([]byte(in), "mcpServers", "frappe")
		if err != nil || found || string(out) != in {
			t.Errorf("%q: got %q found %v err %v", in, out, found, err)
		}
	}
}

func TestRemoveJSONRefused(t *testing.T) {
	for in, want := range map[string]string{
		`{not json`:          "not valid JSON",
		`[]`:                 "not a JSON object",
		`{"mcpServers": []}`: `"mcpServers" is not a JSON object`,
		`{"mcpServers": {"frappe": {}, "frappe": {}}}`:     "more than once",
		`{"mcpServers": {"frappe": {}}, "mcpServers": {}}`: "more than once",
	} {
		if _, _, err := removeJSON([]byte(in), "mcpServers", "frappe"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", in, err, want)
		}
	}
}

func TestCheckRemoveResult(t *testing.T) {
	src := []byte(`{"mcpServers": {"a": {"command": "a"}, "frappe": {}}, "x": 1}`)
	for out, want := range map[string]string{
		`{"mcpServers": {"a": {"command": "a"}, "frappe": {}}, "x": 1}`: "still hold",
		`{"mcpServers": {"a": {"command": "b"}}, "x": 1}`:               "other entries",
		`{"mcpServers": {"a": {"command":"a"}}, "x": 1}`:                "other entries",
		`{"mcpServers": {}, "x": 1}`:                                    "other entries",
		`{"mcpServers": {"a": {"command": "a"}}, "x": 2}`:               "other settings",
		`{"mcpServers": {"a": {"command": "a"}}}`:                       "other settings",
		`{"mcpServers": {"a": {"command": "a"},}, "x": 1}`:              "strict",
		`{"mcpServers": {"a": {"command": "a"}}, "x": 1`:                "valid JSON",
	} {
		err := checkRemoveResult(src, []byte(out), "mcpServers", "frappe", true)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", out, err, want)
		}
	}
	if err := checkRemoveResult(src, []byte(`{"mcpServers": {"a": {"command": "a"}}, "x": 1}`), "mcpServers", "frappe", true); err != nil {
		t.Fatal(err)
	}

	// Comments: those on the entry's own lines may go, every other one must
	// stay, and none may appear.
	src = []byte("{\n  // top\n  \"mcpServers\": {\n    \"a\": 1, // about a\n    // above\n    \"frappe\": 2 /* own */, // own too\n    /* spans\n       lines */\n    \"b\": 3\n  }\n}\n")
	good := "{\n  // top\n  \"mcpServers\": {\n    \"a\": 1, // about a\n    // above\n    /* spans\n       lines */\n    \"b\": 3\n  }\n}\n"
	if err := checkRemoveResult(src, []byte(good), "mcpServers", "frappe", false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ out, want string }{
		{strings.Replace(good, " // about a", "", 1), `lose the comment "// about a"`},
		{strings.Replace(good, "    // above\n", "", 1), `lose the comment "// above"`},
		{strings.Replace(good, "  // top\n", "", 1), `lose the comment "// top"`},
		{strings.Replace(good, "    /* spans\n       lines */\n", "", 1), "lose the comment"},
		{strings.Replace(good, "\"b\": 3", "\"b\": 3 // new", 1), "not there"},
	} {
		if err := checkRemoveResult(src, []byte(tc.out), "mcpServers", "frappe", false); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err %v, want %q", tc.out, err, tc.want)
		}
	}
	// The real removal of that entry passes the check.
	if out, found, err := removeJSON(src, "mcpServers", "frappe"); err != nil || !found || string(out) != good {
		t.Fatalf("%q %v %v", out, found, err)
	}
}

func TestPlanRemoveFileClients(t *testing.T) {
	for _, client := range []string{ClaudeDesktop, Cursor, VSCode} {
		t.Run(client, func(t *testing.T) {
			env := testEnv(t, "windows")
			path, _ := ConfigPath(client, env)
			top := "mcpServers"
			if client == VSCode {
				top = "servers"
			}

			// No file: nothing to remove, nothing created.
			c := mustPlanRemove(t, client, "frappe", env)
			if c.Changed() || c.Replaces || c.Old != nil || c.New != nil || c.Diff() != "" || c.Hint == "" {
				t.Fatalf("missing file: %+v", c)
			}
			if b, err := c.Apply(); err != nil || b != "" {
				t.Fatal(b, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("file created")
			}

			// Present.
			old := "{\r\n  \"" + top + "\": {\r\n    \"a\": {\"command\": \"a\"},\r\n    \"frappe\": {\"command\": \"ffc\"}\r\n  }\r\n}\r\n"
			writeFile(t, path, old)
			c = mustPlanRemove(t, client, "frappe", env)
			want := "{\r\n  \"" + top + "\": {\r\n    \"a\": {\"command\": \"a\"}\r\n  }\r\n}\r\n"
			if !c.Changed() || !c.Replaces || string(c.Old) != old || string(c.New) != want {
				t.Fatalf("present: changed %v replaces %v\n%q", c.Changed(), c.Replaces, c.New)
			}
			if d := c.Diff(); !strings.Contains(d, `-    "frappe": {"command": "ffc"}`) || !strings.Contains(d, "--- "+path) {
				t.Errorf("diff:\n%s", d)
			}

			// Another name: no change, New is Old.
			c = mustPlanRemove(t, client, "other", env)
			if c.Changed() || c.Replaces || string(c.New) != old || c.Diff() != "" {
				t.Fatalf("absent: %+v", c)
			}

			// Unparsable: refused, untouched.
			writeFile(t, path, `{"`+top+`": {"frappe": {}}`)
			if _, err := PlanRemove(client, "frappe", env); err == nil || !strings.Contains(err.Error(), "left untouched") {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestPlanRemoveInvalid(t *testing.T) {
	env := testEnv(t, "linux")
	for _, tc := range []struct{ client, name string }{
		{Cursor, "-x"},
		{Cursor, "a.b"},
		{ClaudeCode, ""},
		{"chatgpt", "frappe"},
	} {
		if _, err := PlanRemove(tc.client, tc.name, env); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s %q: %v", tc.client, tc.name, err)
		}
	}
}

func TestRemoveRoundTrip(t *testing.T) {
	cases := map[string][]string{
		ClaudeDesktop: {
			"{\n  // c\n  \"mcpServers\": {\n    \"a\": {\"command\": \"a\"} // after a\n  },\n  \"x\": true\n}\n",
			"{\n  \"mcpServers\": {\n    \"a\": {}\n    // later\n  }\n}",
		},
		Cursor: {
			crlf("{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"a\", \"args\": []}\n  }\n}\n"),
		},
		VSCode: {
			"{\n\t\"servers\": {\n\t\t\"a\": {\"command\": \"a\"},\n\t},\n\t\"inputs\": [],\n}\n",
			crlf("{\n\t// mine\n\t\"servers\": {\n\t\t/* a */ \"a\": {\"type\": \"http\", \"url\": \"x\"}, // a\n\t\t\"b\": {}\n\t}\n}\n"),
		},
		Codex: {
			"# codex\nmodel = \"o3\"\n\n[mcp_servers.a]\ncommand = \"a\"\n",
			crlf("model = \"o3\"\n\n[mcp_servers.a]\ncommand = \"a\"\n\n[mcp_servers.a.env]\nX = \"1\"\n"),
			"[mcp_servers.a]\ncommand = \"a\"\n\n# end\n",
		},
	}
	roundTrip := func(client, in string) []byte {
		t.Helper()
		env := testEnv(t, "windows")
		path, _ := ConfigPath(client, env)
		writeFile(t, path, in)
		c := mustPlan(t, client, testSrv, env)
		if !c.Changed() || c.Replaces {
			t.Fatalf("%s %q: install plan", client, in)
		}
		writeFile(t, path, string(c.New))
		r := mustPlanRemove(t, client, testSrv.Name, env)
		if !r.Replaces || !r.Changed() {
			t.Fatalf("%s %q: remove plan", client, in)
		}
		return r.New
	}
	for client, ins := range cases {
		for _, in := range ins {
			if got := roundTrip(client, in); string(got) != in {
				t.Errorf("%s: round trip\nremoved:\n%q\nwant:\n%q", client, got, in)
			}
		}
	}

	// A one-line file gets the entry on lines of its own, so the closing
	// brace moves to a new line: the same data and comments, not the same
	// bytes.
	in := `{"mcpServers": {"a": {"command": "a"} /* a */}, "x": 1}`
	got := roundTrip(Cursor, in)
	if want := "{\"mcpServers\": {\"a\": {\"command\": \"a\"} /* a */\n  }, \"x\": 1}"; string(got) != want {
		t.Fatalf("one line:\n%q\nwant\n%q", got, want)
	}
	if entryOf(t, got, "mcpServers", "frappe") != nil || entryOf(t, got, "mcpServers", "a")["command"] != "a" {
		t.Fatalf("one line: %s", got)
	}
}

func TestRemoveTOML(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"middle with a sub-table": {
			in: `model = "o3"

[mcp_servers.a]
command = "a"

[mcp_servers.frappe]
command = "ffc"
args = ["mcp"]

# env for frappe
[mcp_servers.frappe.env]
X = "1"

[mcp_servers.b]
command = "b"
`,
			want: `model = "o3"

[mcp_servers.a]
command = "a"

[mcp_servers.b]
command = "b"
`,
		},
		"first in the file": {
			in:   "[mcp_servers.frappe]\ncommand = \"ffc\"\n\n[other]\nx = 1\n",
			want: "[other]\nx = 1\n",
		},
		"last, a trailing comment stays": {
			in:   "[a]\nx = 1\n\n[mcp_servers.frappe]\ncommand = \"ffc\"\n\n# end\n",
			want: "[a]\nx = 1\n\n# end\n",
		},
		"last, no blank line left at the end": {
			in:   "[a]\nx = 1  \n\n[mcp_servers.frappe]\ncommand = \"ffc\"\n\n\n",
			want: "[a]\nx = 1  \n",
		},
		"only": {
			in:   "[mcp_servers.frappe]\ncommand = \"ffc\"\n",
			want: "",
		},
		"quoted and spaced headers": {
			in:   "[mcp_servers.\"frappe\"]\ncommand = \"ffc\"\n[ mcp_servers . frappe . env ]\nX = \"1\"\n[mcp_servers.frappe-2]\ncommand = \"x\"\n",
			want: "[mcp_servers.frappe-2]\ncommand = \"x\"\n",
		},
		"sub-table only, apart from the main one": {
			in:   "[mcp_servers.frappe.env]\nX = \"1\"\n\n[a]\nx = 1\n\n[mcp_servers.frappe]\ncommand = \"ffc\"\n",
			want: "[a]\nx = 1\n",
		},
		"no final newline": {
			in:   "[a]\nx = 1\n\n[mcp_servers.frappe]\ncommand = \"ffc\"",
			want: "[a]\nx = 1",
		},
		"no final newline, entry first": {
			in:   "[mcp_servers.frappe]\ncommand = \"ffc\"\n\n[a]\nx = 1",
			want: "[a]\nx = 1",
		},
		"a header inside a multi-line string is not one": {
			in:   "[mcp_servers.frappe]\ncommand = \"\"\"\n[not.a.header]\n\"\"\"\n[a]\nx = [\n  \"[mcp_servers.frappe]\",\n]\n",
			want: "[a]\nx = [\n  \"[mcp_servers.frappe]\",\n]\n",
		},
		"BOM": {
			in:   "\xEF\xBB\xBF[a]\nx = 1\n\n[mcp_servers.frappe]\ncommand = \"ffc\"\n",
			want: "\xEF\xBB\xBF[a]\nx = 1\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, conv := range []func(string) string{func(s string) string { return s }, crlf} {
				in, want := conv(tc.in), conv(tc.want)
				out, found, err := removeTOML([]byte(in), "frappe")
				if err != nil || !found {
					t.Fatalf("found %v, err %v", found, err)
				}
				if string(out) != want {
					t.Fatalf("got:\n%q\nwant:\n%q", out, want)
				}
			}
		})
	}
}

func TestRemoveTOMLAbsent(t *testing.T) {
	for _, in := range []string{"", "model = \"o3\"\n", "[mcp_servers.frappe-2]\ncommand = \"x\"\n", "[mcp_servers]\n"} {
		out, found, err := removeTOML([]byte(in), "frappe")
		if err != nil || found || string(out) != in {
			t.Errorf("%q: %q %v %v", in, out, found, err)
		}
	}
}

func TestRemoveTOMLRefused(t *testing.T) {
	for in, want := range map[string]string{
		"mcp_servers = { frappe = { command = \"x\" } }\n":           "dotted key or an inline table",
		"mcp_servers.frappe.command = \"x\"\n":                       "dotted key or an inline table",
		"[mcp_servers]\nfrappe = { command = \"x\" }\n":              "dotted key or an inline table",
		"[[mcp_servers]]\nname = \"frappe\"\n":                       "array of tables",
		"[mcp_servers.frappe]\na = 1\n[mcp_servers.frappe]\nb = 2\n": "more than once",
		"x = \"\"\"\nnot closed\n":                                   "not closed",
		"x = \"\xff\"\n":                                             "UTF-8",
	} {
		if _, _, err := removeTOML([]byte(in), "frappe"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", in, err, want)
		}
	}
}

func TestPlanRemoveCodexApply(t *testing.T) {
	env := testEnv(t, "linux")
	env.Getenv = func(k string) string {
		if k == "CODEX_HOME" {
			return filepath.Join(env.Home, "codex")
		}
		return ""
	}
	path := filepath.Join(env.Home, "codex", "config.toml")
	old := "model = \"o3\"\n\n[mcp_servers.frappe]\ncommand = \"ffc\"\n"
	writeFile(t, path, old)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 10, 6, 9, 5, 7, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	stamp := path + ".ffc-20261006-090507.bak"
	writeFile(t, stamp, "older backup")

	c := mustPlanRemove(t, Codex, "frappe", env)
	if c.Path != path || !c.Changed() || !c.Replaces || string(c.New) != "model = \"o3\"\n" || c.Hint != removeHints[Codex] {
		t.Fatalf("plan: %+v", c)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	backup, err := c.Apply()
	if err != nil || backup != path+".ffc-20261006-090507-1.bak" {
		t.Fatalf("apply: %q %v", backup, err)
	}
	if b, _ := os.ReadFile(backup); string(b) != old {
		t.Error("backup content")
	}
	if b, _ := os.ReadFile(stamp); string(b) != "older backup" {
		t.Error("existing backup overwritten")
	}
	if b, _ := os.ReadFile(path); string(b) != "model = \"o3\"\n" {
		t.Errorf("content %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
			t.Errorf("mode not kept: %v", fi.Mode().Perm())
		}
	}

	// Nothing left to remove.
	c = mustPlanRemove(t, Codex, "frappe", env)
	if c.Changed() || c.Replaces {
		t.Fatal("second plan changes")
	}

	// Changed since Plan.
	writeFile(t, path, old)
	c = mustPlanRemove(t, Codex, "frappe", env)
	writeFile(t, path, old+"# edited\n")
	if _, err := c.Apply(); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("want a conflict, got %v", err)
	}
}

func TestPlanRemoveReadOnly(t *testing.T) {
	env := testEnv(t, "linux")
	path, _ := ConfigPath(Cursor, env)
	old := `{"mcpServers": {"frappe": {}}}`
	writeFile(t, path, old)
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if fi, _ := os.Stat(path); fi.Mode().Perm()&0o200 != 0 {
		t.Skip("cannot make the file read-only here")
	}
	c := mustPlanRemove(t, Cursor, "frappe", env)
	if err := c.Check(); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Check: %v", err)
	}
	if _, err := c.Apply(); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Apply: %v", err)
	}
	if m, _ := filepath.Glob(path + ".ffc-*.bak"); len(m) != 0 {
		t.Fatalf("backup left behind: %v", m)
	}
	if b, _ := os.ReadFile(path); string(b) != old {
		t.Fatal("file changed")
	}
}

func TestPlanRemoveClaudeCode(t *testing.T) {
	state := func(t *testing.T, env Env, s string) {
		writeFile(t, filepath.Join(env.Home, ".claude.json"), s)
	}

	t.Run("present", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		state(t, env, `{"projects": {}, "mcpServers": {"frappe": {"command": "npx", "args": []}, "other": {}}}`)
		before, _ := os.ReadFile(filepath.Join(env.Home, ".claude.json"))
		c := mustPlanRemove(t, ClaudeCode, "frappe", env)
		if !c.Changed() || !c.Replaces || c.Old != nil || c.New != nil || c.Diff() != "" || !c.ToolFound() || c.Hint != removeHints[ClaudeCode] {
			t.Fatalf("change: %+v", c)
		}
		if lines := c.CommandLines(); len(lines) != 1 || lines[0] != "claude mcp remove --scope user frappe" {
			t.Fatalf("lines %q", lines)
		}
		if err := c.Check(); err != nil {
			t.Fatal(err)
		}
		if b, err := c.Apply(); err != nil || b != "" {
			t.Fatal(b, err)
		}
		if len(f.calls) != 1 || strings.Join(f.calls[0], " ") != "/bin/claude mcp remove --scope user frappe" {
			t.Fatalf("calls %q", f.calls)
		}
		if after, _ := os.ReadFile(filepath.Join(env.Home, ".claude.json")); string(after) != string(before) {
			t.Fatal("the state file was written")
		}
	})

	t.Run("absent", func(t *testing.T) {
		for _, s := range []string{"", `{"mcpServers": {"other": {}}}`, `{}`} {
			env, f := claudeEnv(t, "linux", "/bin/claude")
			if s != "" {
				state(t, env, s)
			}
			c := mustPlanRemove(t, ClaudeCode, "frappe", env)
			if c.Changed() || c.Replaces || len(c.Commands) != 0 || c.Check() != nil {
				t.Fatalf("%q: %+v", s, c)
			}
			if _, err := c.Apply(); err != nil || len(f.calls) != 0 {
				t.Fatal(err, f.calls)
			}
		}
	})

	t.Run("unreadable state: not found is success", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		state(t, env, `{not json`)
		f.fail = func([]string) ([]byte, error) {
			return []byte(`No MCP server named "frappe" in user scope`), errors.New("exit status 1")
		}
		c := mustPlanRemove(t, ClaudeCode, "frappe", env)
		if !c.Changed() || c.Replaces || c.Absent || len(c.Commands) != 1 {
			t.Fatalf("change: %+v", c)
		}
		if _, err := c.Apply(); err != nil || len(f.calls) != 1 || !c.Absent {
			t.Fatal(err, f.calls, c.Absent)
		}
	})

	t.Run("unreadable state: removed", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		state(t, env, `{not json`)
		c := mustPlanRemove(t, ClaudeCode, "frappe", env)
		if _, err := c.Apply(); err != nil || len(f.calls) != 1 || c.Absent {
			t.Fatal(err, f.calls, c.Absent)
		}
	})

	t.Run("unreadable state: other failures are errors", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		state(t, env, `{not json`)
		f.fail = func([]string) ([]byte, error) { return []byte("Invalid configuration"), errors.New("exit status 1") }
		c := mustPlanRemove(t, ClaudeCode, "frappe", env)
		if _, err := c.Apply(); err == nil || !strings.Contains(err.Error(), "Invalid configuration") {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("present but claude says not found", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "/bin/claude")
		state(t, env, `{"mcpServers": {"frappe": {}}}`)
		f.fail = func([]string) ([]byte, error) {
			return []byte(`No MCP server named "frappe" in user scope`), errors.New("exit status 1")
		}
		c := mustPlanRemove(t, ClaudeCode, "frappe", env)
		if _, err := c.Apply(); err == nil || !strings.Contains(err.Error(), "No MCP server named") {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("not on PATH", func(t *testing.T) {
		env, f := claudeEnv(t, "linux", "")
		state(t, env, `{"mcpServers": {"frappe": {}}}`)
		c := mustPlanRemove(t, ClaudeCode, "frappe", env)
		if c.ToolFound() {
			t.Fatal("tool found")
		}
		err := c.Check()
		if !errors.Is(err, ErrClaudeNotFound) || !strings.Contains(err.Error(), "remove the server yourself") || !strings.Contains(err.Error(), "claude mcp remove --scope user frappe") {
			t.Fatalf("Check: %v", err)
		}
		if _, err := c.Apply(); !errors.Is(err, ErrClaudeNotFound) || len(f.calls) != 0 {
			t.Fatal(err, f.calls)
		}
	})

	t.Run("windows batch file is run", func(t *testing.T) {
		// No JSON argument, and the name is [A-Za-z0-9_-]: safe for cmd.exe.
		for _, tool := range []string{`C:\npm\claude.cmd`, `C:\npm\claude.BAT`} {
			env, f := claudeEnv(t, "windows", tool)
			state(t, env, `{"mcpServers": {"frappe": {}}}`)
			c := mustPlanRemove(t, ClaudeCode, "frappe", env)
			if err := c.Check(); err != nil {
				t.Fatalf("Check: %v", err)
			}
			if _, err := c.Apply(); err != nil || len(f.calls) != 1 || strings.Join(f.calls[0], " ") != tool+" mcp remove --scope user frappe" {
				t.Fatal(err, f.calls)
			}
		}
		// Install still refuses it.
		env, _ := claudeEnv(t, "windows", `C:\npm\claude.cmd`)
		if err := mustPlan(t, ClaudeCode, testSrv, env).Check(); !errors.Is(err, ErrClaudeBatch) {
			t.Fatalf("install Check: %v", err)
		}
	})

	t.Run("CLAUDE_CONFIG_DIR", func(t *testing.T) {
		env, _ := claudeEnv(t, "linux", "/bin/claude")
		dir := filepath.Join(env.Home, "cc")
		env.Getenv = func(k string) string {
			if k == "CLAUDE_CONFIG_DIR" {
				return dir
			}
			return ""
		}
		writeFile(t, filepath.Join(dir, ".claude.json"), `{"mcpServers": {"frappe": {}}}`)
		c := mustPlanRemove(t, ClaudeCode, "frappe", env)
		if c.Path != filepath.Join(dir, ".claude.json") || !c.Replaces {
			t.Fatalf("change: %+v", c)
		}
	})
}
