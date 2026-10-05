package client

import (
	"context"
	"fmt"
	"strconv"
)

// FieldAccess tells which fields of a DocType (and of its child tables) the
// signed-in user may read, by the rule the desk's activity timeline applies
// to version changes (frappe/desk/form/activity.py readable_permlevels and
// is_field_visible): a field is readable when the meta has it and its
// permission level is one the user's roles may read. ffc also never shows
// Password fields.
type FieldAccess struct {
	// levels are the readable permission levels; nil when the DocType has
	// no permission rows (Frappe then treats every level as readable).
	levels  map[int]bool
	fields  map[string]map[string]accessField // DocType → fieldname → field
	tables  map[string]string                 // table fieldname of the DocType → child DocType
	doctype string
}

type accessField struct {
	fieldtype string
	permlevel int
}

// Readable reports whether the user may read field of the DocType. docstatus
// is no DocField but is always readable.
func (a *FieldAccess) Readable(field string) bool {
	if field == "docstatus" {
		return true
	}
	return a.readable(a.doctype, field)
}

// ReadableRow reports whether the user may read field of a row of table
// (the parent's table fieldname). The table field itself must be readable;
// a child table's levels are the parent's (Meta.get_permissions with
// parenttype).
func (a *FieldAccess) ReadableRow(table, field string) bool {
	child, ok := a.tables[table]
	return ok && a.Readable(table) && a.readable(child, field)
}

func (a *FieldAccess) readable(doctype, field string) bool {
	f, ok := a.fields[doctype][field]
	if !ok || f.fieldtype == "Password" {
		return false
	}
	return a.levels == nil || a.levels[f.permlevel]
}

// ReadableFields reads the DocType's meta (with its child tables, through
// getdoctype) and the user's roles, and returns what the user may read.
//
// Roles: Administrator has every role; anyone else has their Has Role rows
// plus All and Guest. Desk User is not counted (it is given to System Users,
// and the user type is not readable to most users), so a level only Desk User
// reads is treated as unreadable: the error is on the safe side. Level 0 is
// always readable: the user reads the document itself (Frappe adds 0 for a
// user who reads it through a share).
func (c *FrappeClient) ReadableFields(ctx context.Context, doctype string) (*FieldAccess, error) {
	user, err := c.LoggedUser(ctx)
	if err != nil {
		return nil, err
	}
	env, err := c.CallMethodFull(ctx, "frappe.desk.form.load.getdoctype", map[string]interface{}{"doctype": doctype}, true)
	if err != nil {
		return nil, err
	}
	var docs []struct {
		Name   string `json:"name"`
		Fields []struct {
			Fieldname string      `json:"fieldname"`
			Fieldtype string      `json:"fieldtype"`
			Options   string      `json:"options"`
			Permlevel interface{} `json:"permlevel"`
		} `json:"fields"`
		Permissions []map[string]interface{} `json:"permissions"`
	}
	if err := convert(env["docs"], &docs); err != nil || len(docs) == 0 || docs[0].Name != doctype {
		return nil, fmt.Errorf("unexpected response from getdoctype: no meta for %s", doctype)
	}
	a := &FieldAccess{doctype: doctype, fields: map[string]map[string]accessField{}, tables: map[string]string{}}
	for i, d := range docs {
		m := map[string]accessField{}
		for _, f := range d.Fields {
			m[f.Fieldname] = accessField{fieldtype: f.Fieldtype, permlevel: permlevel(f.Permlevel)}
			if i == 0 && (f.Fieldtype == "Table" || f.Fieldtype == "Table MultiSelect") && f.Options != "" {
				a.tables[f.Fieldname] = f.Options
			}
		}
		a.fields[d.Name] = m
	}
	rows := docs[0].Permissions
	if len(rows) == 0 {
		return a, nil
	}
	admin := user == "Administrator"
	have := map[string]bool{roleAll: true, roleGuest: true}
	if !admin {
		roles, err := c.UserRoles(ctx, user)
		if err != nil {
			return nil, err
		}
		for _, r := range roles {
			have[r] = true
		}
	}
	a.levels = map[int]bool{0: true}
	for _, row := range rows {
		role, _ := row["role"].(string)
		if (admin || have[role]) && truthy(row["read"]) {
			a.levels[permlevel(row["permlevel"])] = true
		}
	}
	return a, nil
}

// permlevel reads a permission level; a value that is not a number counts
// as level -1, which no role reads.
func permlevel(v interface{}) int {
	if v == nil {
		return 0
	}
	f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
	if err != nil {
		return -1
	}
	return int(f)
}
