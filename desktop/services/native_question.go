package services

import (
	"errors"
	"runtime"
	"time"
)

// questionButton is one button of a native yes/no question.
type questionButton struct {
	label     string
	yes       bool
	isDefault bool
	isCancel  bool
}

// questionButtons are the buttons of a native yes/no question on goos.
//
// On Windows, Wails v3 (beta.28, pkg/application/dialogs_windows.go) shows a
// question as a MessageBox with the system's own Yes and No buttons and runs
// only the callback of a button labelled exactly "Yes" or "No": any other
// label never answers, and the question would wait out its timeout. So the
// labels are "Yes" and "No" there (the person sees Windows' translated
// buttons), the title must read as a yes/no question, and "No" is the
// default when defaultYes is false (MB_DEFBUTTON2, set only for a default
// button labelled "no"). Elsewhere the buttons carry the app's own labels.
func questionButtons(goos, yesLabel, noLabel string, defaultYes bool) []questionButton {
	if goos == "windows" {
		yesLabel, noLabel = "Yes", "No"
	}
	return []questionButton{
		{label: yesLabel, yes: true, isDefault: defaultYes},
		{label: noLabel, isDefault: !defaultYes, isCancel: true},
	}
}

// askQuestion shows a native yes/no question the web view cannot answer.
// false means No, the dialog closed without a button, or no answer within
// questionWait.
func (h *WailsHost) askQuestion(title, message, yesLabel, noLabel string, defaultYes bool) (bool, error) {
	if h.App == nil {
		return false, errors.New("the app is not running")
	}
	d := h.App.Dialog.Question().SetTitle(title).SetMessage(message)
	if w := h.App.Window.Current(); w != nil {
		d.AttachToWindow(w)
	}
	// Show does not wait for the answer on every OS (macOS shows the alert
	// asynchronously, Linux runs the button callbacks in goroutines), so the
	// answer comes through a channel.
	answer := make(chan bool, 2)
	for _, b := range questionButtons(runtime.GOOS, yesLabel, noLabel, defaultYes) {
		yes := b.yes
		btn := d.AddButton(b.label).OnClick(func() { answer <- yes })
		if b.isDefault {
			btn.SetAsDefault()
		}
		if b.isCancel {
			btn.SetAsCancel()
		}
	}
	d.Show()
	select {
	case ok := <-answer:
		return ok, nil
	case <-time.After(questionWait):
		return false, nil
	}
}

// questionWait bounds the wait for an answer to a native question.
const questionWait = 10 * time.Minute
