package client

import (
	"context"
	"fmt"
)

// FormField is a field of a DocType's form meta, with what decides whether
// a user may edit it.
type FormField struct {
	Fieldname     string
	Fieldtype     string
	Options       string // the child DocType of a table field
	ReadOnly      bool
	Hidden        bool
	AllowOnSubmit bool
	IsVirtual     bool
	SetOnlyOnce   bool
	FetchFrom     string
	FetchIfEmpty  bool
}

// FormMeta is a DocType's form meta: its fields in form order.
type FormMeta struct {
	Name   string
	Fields []FormField
}

// FormMetas reads the meta of a DocType and of its child tables, by
// DocType, from the desk's frappe.desk.form.load.getdoctype (custom fields
// and property setters included).
func (c *FrappeClient) FormMetas(ctx context.Context, doctype string) (map[string]*FormMeta, error) {
	env, err := c.CallMethodFull(ctx, "frappe.desk.form.load.getdoctype", map[string]interface{}{"doctype": doctype}, true)
	if err != nil {
		return nil, fmt.Errorf("reading the %s meta: %w", doctype, err)
	}
	var docs []struct {
		Name   string `json:"name"`
		Fields []struct {
			Fieldname     string      `json:"fieldname"`
			Fieldtype     string      `json:"fieldtype"`
			Options       interface{} `json:"options"`
			ReadOnly      interface{} `json:"read_only"`
			Hidden        interface{} `json:"hidden"`
			AllowOnSubmit interface{} `json:"allow_on_submit"`
			IsVirtual     interface{} `json:"is_virtual"`
			SetOnlyOnce   interface{} `json:"set_only_once"`
			FetchFrom     interface{} `json:"fetch_from"`
			FetchIfEmpty  interface{} `json:"fetch_if_empty"`
		} `json:"fields"`
	}
	if err := convert(env["docs"], &docs); err != nil || len(docs) == 0 {
		return nil, fmt.Errorf("unexpected response from getdoctype: no meta for %s", doctype)
	}
	str := func(v interface{}) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	out := make(map[string]*FormMeta, len(docs))
	for _, d := range docs {
		m := &FormMeta{Name: d.Name}
		for _, f := range d.Fields {
			m.Fields = append(m.Fields, FormField{
				Fieldname: f.Fieldname, Fieldtype: f.Fieldtype, Options: str(f.Options),
				ReadOnly: truthy(f.ReadOnly), Hidden: truthy(f.Hidden), AllowOnSubmit: truthy(f.AllowOnSubmit),
				IsVirtual: truthy(f.IsVirtual), SetOnlyOnce: truthy(f.SetOnlyOnce),
				FetchFrom: str(f.FetchFrom), FetchIfEmpty: truthy(f.FetchIfEmpty),
			})
		}
		out[d.Name] = m
	}
	if out[doctype] == nil {
		return nil, fmt.Errorf("unexpected response from getdoctype: no meta for %s", doctype)
	}
	return out, nil
}
