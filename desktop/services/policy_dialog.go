package services

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

var _ PolicyConfirmer = (*WailsHost)(nil)

// policyDialogKeys maps a change that opens access to its dialog string. A
// kind missing here is still shown, by its generic line.
var policyDialogKeys = map[string]string{
	policyReadOnlyOff:         "policyReadOnlyOff",
	policyConfirmLooser:       "policyConfirmLooser",
	policyAllowToolsAdded:     "policyAllowToolsAdded",
	policyAllowToolsLifted:    "policyAllowToolsLifted",
	policyAllowDoctypesAdded:  "policyAllowDoctypesAdded",
	policyAllowDoctypesLifted: "policyAllowDoctypesLifted",
	policyDenyDoctypesRemoved: "policyDenyDoctypesRemoved",
	policyAllowMethodsAdded:   "policyAllowMethodsAdded",
	policyAllowMethodsLifted:  "policyAllowMethodsLifted",
	policyDenyMethodsRemoved:  "policyDenyMethodsRemoved",
	policySensitiveUnlocked:   "policySensitiveUnlocked",
	policyDeniedUnlocked:      "policyDeniedUnlocked",
	policyMethodsEnabled:      "policyMethodsEnabled",
}

// policyDialogValues is how many entries of one change the dialog lists.
const policyDialogValues = 12

// policyChangeText is the dialog's description of the changes that open
// access to site, in the current language.
func policyChangeText(site string, opens []PolicyChange) string {
	var b strings.Builder
	fmt.Fprintf(&b, dialogT("policyBody"), isolate(policyText(site)))
	b.WriteString("\n")
	for _, c := range dangerousFirst(opens) {
		key, ok := policyDialogKeys[c.Kind]
		if !ok {
			key = "policyOther"
		}
		b.WriteString("\n• ")
		switch c.Kind {
		case policyConfirmLooser:
			from, to := "", ""
			if len(c.Values) == 2 {
				from, to = dialogT("policyConfirm-"+c.Values[0]), dialogT("policyConfirm-"+c.Values[1])
			}
			fmt.Fprintf(&b, dialogT(key), from, to)
		case policyReadOnlyOff, policyMethodsEnabled:
			b.WriteString(dialogT(key))
		default:
			b.WriteString(dialogT(key))
			vals := c.Values
			more := 0
			if len(vals) > policyDialogValues {
				vals, more = vals[:policyDialogValues], len(vals)-policyDialogValues
			}
			for i, v := range vals {
				if i == 0 {
					b.WriteString(" ")
				} else {
					b.WriteString(", ")
				}
				b.WriteString(isolate(policyText(v)))
			}
			if more > 0 {
				b.WriteString(" ")
				fmt.Fprintf(&b, dialogT("policyMore"), more)
			}
		}
	}
	b.WriteString("\n\n")
	b.WriteString(dialogT("policyWarning"))
	return b.String()
}

// policyDangerous are the changes that unlock what ffc refuses by default;
// the dialog lists them first, so a long list cannot push them out of view.
var policyDangerous = []string{policySensitiveUnlocked, policyDeniedUnlocked, policyReadOnlyOff, policyMethodsEnabled}

func dangerousFirst(opens []PolicyChange) []PolicyChange {
	out := make([]PolicyChange, 0, len(opens))
	for _, k := range policyDangerous {
		for _, c := range opens {
			if c.Kind == k {
				out = append(out, c)
			}
		}
	}
	for _, c := range opens {
		if !slices.Contains(policyDangerous, c.Kind) {
			out = append(out, c)
		}
	}
	return out
}

// policyText makes a site name or list entry safe to show in the dialog:
// control, bidi and invisible characters are removed (a hand-edited file
// may hold them).
func policyText(s string) string {
	return dialogText(text.Sanitize(s))
}

// ConfirmPolicyChange asks "Open access to this site?" with each change that
// opens access. Cancel (No on Windows) is the default; false means the
// person declined or closed the dialog.
func (h *WailsHost) ConfirmPolicyChange(site string, opens []PolicyChange) (bool, error) {
	return h.askQuestion(dialogT("policyTitle"), policyChangeText(site, opens), dialogT("policyApply"), dialogT("dropCancel"), false)
}

// ConfirmRemoveRules asks "Remove this site and its safety rules?". Cancel
// (No on Windows) is the default; false means the person declined or closed
// the dialog.
func (h *WailsHost) ConfirmRemoveRules(site string) (bool, error) {
	return h.askQuestion(dialogT("removeRulesTitle"), fmt.Sprintf(dialogT("removeRulesBody"), isolate(policyText(site))), dialogT("removeRulesApply"), dialogT("dropCancel"), false)
}
