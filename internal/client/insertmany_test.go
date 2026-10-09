package client

import (
	"strings"
	"testing"
)

func TestInsertManyDocs(t *testing.T) {
	in := []map[string]interface{}{
		{"title": "a"},
		{"doctype": "ToDo", "title": "b"},
	}
	out, err := InsertManyDocs("ToDo", in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0]["doctype"] != "ToDo" || out[1]["doctype"] != "ToDo" || out[0]["title"] != "a" {
		t.Errorf("out = %v", out)
	}
	if _, ok := in[0]["doctype"]; ok {
		t.Error("the caller's item was modified")
	}

	for name, tc := range map[string]struct {
		docs []map[string]interface{}
		want string
	}{
		"other doctype":   {[]map[string]interface{}{{"title": "a"}, {"doctype": "Note"}}, `item 2 has doctype Note, not "ToDo"`},
		"non-string type": {[]map[string]interface{}{{"doctype": 5}}, `item 1 has doctype 5`},
		"child row":       {[]map[string]interface{}{{"parent": "P-1", "parenttype": "Parent"}}, "item 1 has parent and parenttype"},
	} {
		if _, err := InsertManyDocs("ToDo", tc.docs); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}

	over := make([]map[string]interface{}, MaxInsertMany+1)
	for i := range over {
		over[i] = map[string]interface{}{}
	}
	if _, err := InsertManyDocs("ToDo", over[:MaxInsertMany]); err != nil {
		t.Errorf("%d documents: %v", MaxInsertMany, err)
	}
	if _, err := InsertManyDocs("ToDo", over); err == nil || !strings.Contains(err.Error(), "201 documents exceed the 200") {
		t.Errorf("201 documents: err = %v", err)
	}
}
