package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"strings"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/attach"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// EventChatAttachments reports files dropped on a conversation's composer:
// Go reads the dropped paths itself (WindowFilesDropped), so the web view
// never hands a path to a binding.
const EventChatAttachments = "chat:attachments"

func init() {
	application.RegisterEvent[ChatAttachments](EventChatAttachments)
}

// attachFilter is what the attach dialog offers.
const attachFilter = "*.txt;*.md;*.log;*.csv;*.tsv;*.json;*.xlsx;*.png;*.jpg;*.jpeg;*.webp;*.gif"

// StagedAttachment is a file attached to a conversation's next message, or
// (in ChatMessage) to a sent one. Kind is "text" or "image".
type StagedAttachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	Kind string `json:"kind"`
}

// AttachResult is the outcome of attaching files: the files staged and, for
// each one refused, why (shown one by one). Cancelled means the person closed
// the dialog.
type AttachResult struct {
	Attachments []StagedAttachment `json:"attachments"`
	Errors      []string           `json:"errors"`
	Cancelled   bool               `json:"cancelled,omitempty"`
}

// ChatAttachments is the payload of EventChatAttachments: what AddAttachment
// would answer, for the conversation the files were dropped on.
type ChatAttachments struct {
	ConvID      string             `json:"convID"`
	Attachments []StagedAttachment `json:"attachments"`
	Errors      []string           `json:"errors"`
}

// AttachDialogs is what attaching needs from the desktop shell: a dialog
// that picks files to open. No paths means the person cancelled.
type AttachDialogs interface {
	OpenFilesDialog(title, filterName, pattern string) ([]string, error)
}

var _ AttachDialogs = (*WailsHost)(nil)

// OpenFilesDialog asks for files to open.
func (h *WailsHost) OpenFilesDialog(title, filterName, pattern string) ([]string, error) {
	if h.App == nil {
		return nil, errors.New("the app is not running")
	}
	d := h.App.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                   title,
		Message:                 title,
		CanChooseFiles:          true,
		AllowsMultipleSelection: true,
		Filters:                 []application.FileFilter{{DisplayName: filterName, Pattern: pattern}},
		Window:                  h.App.Window.Current(),
	})
	paths, err := d.PromptForMultipleSelection()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cancel") {
			return nil, nil
		}
		return nil, err
	}
	return paths, nil
}

func stagedOf(a store.Attachment) StagedAttachment {
	kind := attach.KindText
	if attach.IsImageMime(a.Mime) {
		kind = attach.KindImage
	}
	return StagedAttachment{ID: a.ID, Name: a.Name, Mime: a.Mime, Size: a.Size, Kind: kind}
}

// AddAttachment opens a file dialog and attaches the chosen files to the
// next message of a conversation. Each file is checked (see package attach);
// one refused does not stop the others.
func (a *AssistantService) AddAttachment(convID string) (AttachResult, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return AttachResult{}, err
	}
	defer done()
	if _, err := st.GetConversation(convID); err != nil {
		return AttachResult{}, wrapStoreErr(err)
	}
	dlg, ok := a.host.(AttachDialogs)
	if !ok {
		return AttachResult{}, errNoDialogs
	}
	paths, err := dlg.OpenFilesDialog("Attach files", "Text, CSV, JSON, XLSX or images", attachFilter)
	if err != nil {
		return AttachResult{}, newError(CodeFailed, "The file dialog failed.", err)
	}
	if len(paths) == 0 {
		return AttachResult{Attachments: []StagedAttachment{}, Errors: []string{}, Cancelled: true}, nil
	}
	return a.attachPaths(st, convID, paths)
}

// HandleDroppedFiles attaches files dropped on the composer of convID (the
// drop target's data-conv-id) and reports the outcome as
// EventChatAttachments. A function, not a method, so Wails does not bind it:
// paths come from the window's drop event only.
func HandleDroppedFiles(a *AssistantService, convID string, paths []string) {
	if a == nil || convID == "" || len(paths) == 0 {
		return
	}
	res, err := func() (AttachResult, error) {
		_, st, done, err := a.enter()
		if err != nil {
			return AttachResult{}, err
		}
		defer done()
		if _, err := st.GetConversation(convID); err != nil {
			return AttachResult{}, wrapStoreErr(err)
		}
		return a.attachPaths(st, convID, paths)
	}()
	if err != nil {
		res = AttachResult{Attachments: []StagedAttachment{}, Errors: []string{toServiceError(err).Message}}
	}
	a.host.Emit(EventChatAttachments, ChatAttachments{ConvID: convID, Attachments: res.Attachments, Errors: res.Errors})
}

func (a *AssistantService) attachPaths(st *store.Store, convID string, paths []string) (AttachResult, error) {
	res := AttachResult{Attachments: []StagedAttachment{}, Errors: []string{}}
	for _, p := range paths {
		f, err := attach.ReadFile(p)
		if err == nil {
			var s StagedAttachment
			if s, err = a.stage(st, convID, f); err == nil {
				res.Attachments = append(res.Attachments, s)
				continue
			}
		}
		var ae *attach.Error
		var se *Error
		switch {
		case errors.As(err, &ae):
			res.Errors = append(res.Errors, ae.Msg)
		case errors.As(err, &se) && se.Code == CodeInvalid:
			res.Errors = append(res.Errors, se.Message)
		default:
			return res, err
		}
	}
	return res, nil
}

// AddPastedImage attaches an image pasted in the composer: base64 (a data:
// URL prefix is allowed), at most 5 MB decoded, PNG, JPEG, WebP or GIF.
func (a *AssistantService) AddPastedImage(convID, b64 string) (StagedAttachment, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return StagedAttachment{}, err
	}
	defer done()
	if _, err := st.GetConversation(convID); err != nil {
		return StagedAttachment{}, wrapStoreErr(err)
	}
	if i := strings.Index(b64, ";base64,"); strings.HasPrefix(b64, "data:") && i > 0 {
		b64 = b64[i+len(";base64,"):]
	}
	if len(b64) > base64.StdEncoding.EncodedLen(attach.MaxPastedBytes) {
		return StagedAttachment{}, invalid("attachment", "The pasted image is larger than 5 MB.")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return StagedAttachment{}, invalid("attachment", "The pasted image could not be read.")
	}
	f, err := attach.ParseImage(data)
	if err != nil {
		return StagedAttachment{}, attachErr(err)
	}
	return a.stage(st, convID, f)
}

// ListAttachments returns the attachments staged for a conversation's next
// message.
func (a *AssistantService) ListAttachments(convID string) ([]StagedAttachment, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return nil, err
	}
	defer done()
	rows, err := st.StagedAttachments(convID)
	if err != nil {
		return nil, wrapStoreErr(err)
	}
	out := make([]StagedAttachment, 0, len(rows))
	for _, r := range rows {
		out = append(out, stagedOf(r))
	}
	return out, nil
}

// RemoveAttachment removes a staged attachment of a conversation.
func (a *AssistantService) RemoveAttachment(convID, id string) error {
	_, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	if err := st.DeleteStagedAttachment(convID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &Error{Code: CodeNotFound, Message: "That attachment is no longer there.", Field: "attachment"}
		}
		return wrapStoreErr(err)
	}
	return nil
}

func attachErr(err error) error {
	var ae *attach.Error
	if errors.As(err, &ae) {
		return invalid("attachment", ae.Msg)
	}
	return newError(CodeFailed, "The file could not be attached.", err)
}

// stage stores a checked file as a staged attachment of convID. An image is
// refused when the conversation's model does not take images.
func (a *AssistantService) stage(st *store.Store, convID string, f attach.File) (StagedAttachment, error) {
	if f.Kind == attach.KindImage {
		conv, err := st.GetConversation(convID)
		if err != nil {
			return StagedAttachment{}, wrapStoreErr(err)
		}
		if !a.convTakesImages(st, conv) {
			return StagedAttachment{}, errNoImages(f.Name)
		}
	}
	row, err := st.AddAttachment(store.Attachment{ConvID: convID, Name: f.Name, Mime: f.Mime, Size: f.Size, SHA256: f.SHA256, Text: f.Text}, f.Data, attach.MaxPerMessage)
	if errors.Is(err, store.ErrLimit) {
		return StagedAttachment{}, invalid("attachment", fmt.Sprintf("A message can carry at most %d attachments.", attach.MaxPerMessage))
	}
	if err != nil {
		return StagedAttachment{}, wrapStoreErr(err)
	}
	return stagedOf(row), nil
}

func errNoImages(name string) *Error {
	return invalid("attachment", name+": the model of this conversation does not take images. Choose a model that reads images, or attach text.")
}

// convTakesImages reports whether the conversation's provider and model take
// images.
func (a *AssistantService) convTakesImages(st *store.Store, conv store.Conversation) bool {
	p, err := a.provider(st, conv.ProviderID)
	if err != nil {
		return false
	}
	model := conv.Model
	if model == "" {
		model = defaultModelOf(p)
	}
	return takesImages(p.Kind, model)
}

// takesImages reports whether a model of a provider kind takes image input.
// It is a table of model families known to read images, kept conservative:
// anything not listed, every OpenRouter, custom and local model included,
// takes none. (OpenRouter's model list has input modalities, but the app
// does not read them yet; local servers need a profile switch that does not
// exist yet.)
func takesImages(kind, model string) bool {
	m := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
	hasAny := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(m, s) {
				return true
			}
		}
		return false
	}
	hasPrefix := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(m, p) {
				return true
			}
		}
		return false
	}
	switch kind {
	case KindAnthropic:
		// Every Claude model since Claude 3 reads images.
		return strings.HasPrefix(m, "claude-") && !hasPrefix("claude-2", "claude-instant", "claude-1")
	case KindOpenAI:
		if hasAny("audio", "realtime", "transcribe", "tts", "search", "embedding", "codex", "image", "moderation") {
			return false
		}
		if hasPrefix("o1-mini", "o1-preview", "o3-mini") {
			return false
		}
		return hasPrefix("gpt-4o", "gpt-4.1", "gpt-4.5", "gpt-5", "chatgpt-4o", "o1", "o3", "o4")
	case KindGemini:
		if hasAny("embedding", "tts", "audio", "live", "aqa") {
			return false
		}
		return hasPrefix("gemini-1.5", "gemini-2", "gemini-3")
	}
	return false
}

// chatAttachment is the chip of an attachment part: from its row, or, when
// the row is gone (an imported conversation), from the part itself.
func chatAttachment(rows map[string]store.Attachment, id, name, mime string) StagedAttachment {
	if r, ok := rows[id]; ok {
		return stagedOf(r)
	}
	kind := attach.KindText
	if attach.IsImageMime(mime) {
		kind = attach.KindImage
	}
	return StagedAttachment{ID: id, Name: name, Mime: mime, Kind: kind}
}

// attachmentName reads the file name back from a wrapped attachment text.
func attachmentName(wrapped string) string {
	const head = `<attachment name="`
	if !strings.HasPrefix(wrapped, head) {
		return "attachment"
	}
	rest := wrapped[len(head):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return "attachment"
	}
	return html.UnescapeString(rest[:end])
}

// wrapAttachment marks the text of an attached file as data: the name is
// escaped as an attribute, the text with escapeUntrusted, so nothing in
// either can close the wrapper.
func wrapAttachment(name, text string) string {
	return `<attachment name="` + html.EscapeString(name) + `" untrusted="true">` + "\n" + escapeUntrusted(text) + "\n</attachment>"
}

// attachmentParts turns the staged attachments of a message into its parts:
// the text of each file wrapped as data (see wrapAttachment), each image as
// an Image part. It refuses more than MaxPerMessage files, more than
// MaxMessageChars of text, and images for a model that takes none.
func attachmentParts(atts []store.Attachment, images bool) ([]llm.Part, error) {
	if len(atts) > attach.MaxPerMessage {
		return nil, invalid("attachments", fmt.Sprintf("A message can carry at most %d attachments.", attach.MaxPerMessage))
	}
	var parts []llm.Part
	chars := 0
	for _, at := range atts {
		if attach.IsImageMime(at.Mime) {
			if !images {
				return nil, errNoImages(at.Name)
			}
			parts = append(parts, llm.Image{AttachmentID: at.ID, MediaType: at.Mime})
			continue
		}
		chars += utf8.RuneCountInString(at.Text)
		if chars > attach.MaxMessageChars {
			return nil, invalid("attachments", "The attachments of one message can hold at most 400 000 characters of text in all. Send some of them in another message.")
		}
		parts = append(parts, llm.Text{Text: wrapAttachment(at.Name, at.Text), AttachmentID: at.ID})
	}
	return parts, nil
}

// attachmentImages resolves the Image parts of a conversation from the
// store: only that conversation's attachments.
type attachmentImages struct {
	st     *store.Store
	convID string
}

func (r attachmentImages) ImageData(_ context.Context, id string) ([]byte, error) {
	return r.st.AttachmentData(r.convID, id)
}

// dropImages replaces the Image parts of the history with a note, for a
// model that takes no images (the conversation's model was changed after an
// image was sent). The stored rows are not changed.
func dropImages(in []llm.Message) []llm.Message {
	out := make([]llm.Message, len(in))
	for i, m := range in {
		out[i] = m
		has := false
		for _, p := range m.Parts {
			if _, ok := p.(llm.Image); ok {
				has = true
				break
			}
		}
		if !has {
			continue
		}
		parts := make([]llm.Part, 0, len(m.Parts))
		for _, p := range m.Parts {
			if _, ok := p.(llm.Image); ok {
				p = llm.Text{Text: "[an image was attached here; this model does not take images]"}
			}
			parts = append(parts, p)
		}
		out[i].Parts = parts
	}
	return out
}
