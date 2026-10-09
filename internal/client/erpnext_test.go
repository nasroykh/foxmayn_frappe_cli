package client

import (
	"reflect"
	"testing"
)

func TestMapTable(t *testing.T) {
	for _, major := range []int{15, 16} {
		if !SupportedERPNext(major) {
			t.Errorf("major %d not supported", major)
		}
		if got := len(MapPairs(major)); got != 8 {
			t.Errorf("major %d: %d pairs, want 8", major, got)
		}
		if m, ok := MapMethod(major, "Sales Order", "Sales Invoice"); !ok || m != "erpnext.selling.doctype.sales_order.sales_order.make_sales_invoice" {
			t.Errorf("major %d: Sales Order -> Sales Invoice = %q, %v", major, m, ok)
		}
	}
	for _, major := range []int{0, 14, 17} {
		if SupportedERPNext(major) {
			t.Errorf("major %d supported", major)
		}
		if _, ok := MapMethod(major, "Sales Order", "Sales Invoice"); ok {
			t.Errorf("major %d maps", major)
		}
	}
	// Sales Order -> Purchase Order answers a list, so it is not offered.
	if _, ok := MapMethod(16, "Sales Order", "Purchase Order"); ok {
		t.Error("Sales Order -> Purchase Order is mapped")
	}
	if got := MapPairs(16)[0].String(); got != "Delivery Note -> Sales Invoice" {
		t.Errorf("first pair = %q", got)
	}
}

func TestServerInfoMajor(t *testing.T) {
	var nilInfo *ServerInfo
	info := &ServerInfo{Apps: map[string]AppVersion{"erpnext": {Version: "15.121.6"}, "frappe": {Version: "develop"}}}
	for _, tc := range []struct {
		info *ServerInfo
		app  string
		want int
	}{{info, "erpnext", 15}, {info, "frappe", 0}, {info, "hrms", 0}, {nilInfo, "erpnext", 0}} {
		if got := tc.info.Major(tc.app); got != tc.want {
			t.Errorf("Major(%q) = %d, want %d", tc.app, got, tc.want)
		}
	}
}

func TestInsertableCopy(t *testing.T) {
	// The shape of a mapper's answer: no top-level name, child rows with a
	// null parent and their place in the table.
	draft := map[string]interface{}{
		"doctype": "Sales Invoice", "__islocal": 1, "__unsaved": 1, "customer": "C1", "docstatus": 0,
		"items": []interface{}{
			map[string]interface{}{"doctype": "Sales Invoice Item", "name": nil, "parent": nil, "parenttype": "Sales Invoice", "parentfield": "items",
				"idx": 1, "docstatus": 0, "__islocal": 1, "__temporary_name": "row1", "item_code": "I1"},
			map[string]interface{}{"doctype": "Sales Invoice Item", "name": "row-2", "__unsaved": 1, "item_code": "I2", "idx": 2},
		},
		"taxes": []interface{}{}, "note": nil,
		// Data that only looks like rows is not touched.
		"json_field": []interface{}{map[string]interface{}{"__keep": 1, "name": ""}},
		"tags":       []interface{}{"a", "b"},
	}
	got := InsertableCopy(draft)
	want := map[string]interface{}{
		"doctype": "Sales Invoice", "customer": "C1", "docstatus": 0, "note": nil, "taxes": []interface{}{},
		"items": []interface{}{
			map[string]interface{}{"doctype": "Sales Invoice Item", "parent": nil, "parenttype": "Sales Invoice", "parentfield": "items",
				"idx": 1, "docstatus": 0, "item_code": "I1"},
			map[string]interface{}{"doctype": "Sales Invoice Item", "name": "row-2", "item_code": "I2", "idx": 2},
		},
		"json_field": []interface{}{map[string]interface{}{"__keep": 1, "name": ""}},
		"tags":       []interface{}{"a", "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InsertableCopy = %v\nwant %v", got, want)
	}
	// The draft the caller printed is left as it was.
	if _, ok := draft["__islocal"]; !ok {
		t.Errorf("draft changed: %v", draft)
	}
	// A real name is kept; an empty one is not.
	if got := InsertableCopy(map[string]interface{}{"name": "SINV-0001"}); got["name"] != "SINV-0001" {
		t.Errorf("name = %v", got["name"])
	}
	if got := InsertableCopy(map[string]interface{}{"name": ""}); len(got) != 0 {
		t.Errorf("empty name kept: %v", got)
	}
}

func TestPaymentTable(t *testing.T) {
	for _, major := range []int{15, 16} {
		if m, ok := PaymentMethod(major); !ok || m != "erpnext.accounts.doctype.payment_entry.payment_entry.get_payment_entry" {
			t.Errorf("major %d: payment method = %q, %v", major, m, ok)
		}
	}
	for _, major := range []int{0, 14, 17} {
		if _, ok := PaymentMethod(major); ok {
			t.Errorf("major %d has a payment method", major)
		}
	}
	for _, dt := range []string{"Sales Invoice", "Sales Order", "Purchase Invoice", "Purchase Order", "Dunning"} {
		if !CanPayAgainst(dt) {
			t.Errorf("cannot pay against %s", dt)
		}
	}
	for _, dt := range []string{"Quotation", "Journal Entry", "Payment Entry", ""} {
		if CanPayAgainst(dt) {
			t.Errorf("can pay against %q", dt)
		}
	}
}
