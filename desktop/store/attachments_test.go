package store

import (
	"bytes"
	"errors"
	"testing"
)

func TestAttachmentsLifecycle(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	other := mustConv(t, s, "other")

	txt, err := s.AddAttachment(Attachment{ConvID: c.ID, Name: "a.txt", Mime: "text/plain", Size: 3, SHA256: "h1", Text: "abc"}, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	img, err := s.AddAttachment(Attachment{ConvID: c.ID, Name: "b.png", Mime: "image/png", Size: 4, SHA256: "h2"}, []byte{1, 2, 3, 4}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAttachment(Attachment{ConvID: c.ID, Name: "c.txt", Mime: "text/plain", SHA256: "h3"}, nil, 2); !errors.Is(err, ErrLimit) {
		t.Fatalf("third staged = %v, want ErrLimit", err)
	}
	if _, err := s.AddAttachment(Attachment{ConvID: "nope", Name: "c.txt", Mime: "text/plain", SHA256: "h3"}, nil, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown conversation = %v, want ErrNotFound", err)
	}

	staged, err := s.StagedAttachments(c.ID)
	if err != nil || len(staged) != 2 || staged[0].ID != txt.ID || staged[1].ID != img.ID || staged[0].Text != "abc" {
		t.Fatalf("staged = %+v, %v", staged, err)
	}
	if got, err := s.AttachmentData(c.ID, img.ID); err != nil || !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("data = %v, %v", got, err)
	}

	// Another conversation sees none of them.
	if _, err := s.StagedByID(other.ID, []string{txt.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("StagedByID from another conversation = %v", err)
	}
	if _, err := s.AttachmentData(other.ID, img.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AttachmentData from another conversation = %v", err)
	}
	if err := s.DeleteStagedAttachment(other.ID, txt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete from another conversation = %v", err)
	}
	if _, err := s.AppendUserMessage(other.ID, "user", `[]`, []string{txt.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("link from another conversation = %v", err)
	}
	if msgs, _ := s.ListMessages(other.ID); len(msgs) != 0 {
		t.Fatalf("a refused link left a message: %+v", msgs)
	}

	got, err := s.StagedByID(c.ID, []string{img.ID, txt.ID})
	if err != nil || len(got) != 2 || got[0].ID != img.ID {
		t.Fatalf("StagedByID = %+v, %v", got, err)
	}
	m, err := s.AppendUserMessage(c.ID, "user", `[{"type":"text","text":"hi"}]`, []string{txt.ID, img.ID})
	if err != nil {
		t.Fatal(err)
	}
	if staged, _ := s.StagedAttachments(c.ID); len(staged) != 0 {
		t.Fatalf("still staged after send: %+v", staged)
	}
	byMsg, err := s.MessageAttachments(c.ID)
	if err != nil || len(byMsg[m.ID]) != 2 || byMsg[m.ID][0].Name != "a.txt" {
		t.Fatalf("message attachments = %+v, %v", byMsg, err)
	}
	// A sent attachment cannot be sent again or removed.
	if _, err := s.StagedByID(c.ID, []string{txt.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sent attachment still staged: %v", err)
	}
	if err := s.DeleteStagedAttachment(c.ID, txt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete a sent attachment = %v", err)
	}
	if _, err := s.AppendUserMessage(c.ID, "user", `[]`, []string{txt.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("link a sent attachment = %v", err)
	}
	if msgs, _ := s.ListMessages(c.ID); len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}

	// Removing a staged one frees its slot; deleting the conversation
	// removes the rest.
	a3, err := s.AddAttachment(Attachment{ConvID: c.ID, Name: "c.txt", Mime: "text/plain", SHA256: "h3", Text: "x"}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteStagedAttachment(c.ID, a3.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "attachments"); n != 0 {
		t.Fatalf("attachments left after delete: %d", n)
	}
}

// The text of an attached file is stored under "content", which the search
// index does not read.
func TestAttachmentTextNotIndexed(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "c")
	if _, err := s.AppendMessage(c.ID, "user",
		`[{"type":"attachment","attachment_id":"a1","content":"<attachment name=\"x\">zebrafile</attachment>"},{"type":"text","text":"question"}]`); err != nil {
		t.Fatal(err)
	}
	if hits, err := s.Search("zebrafile", 0); err != nil || len(hits) != 0 {
		t.Fatalf("attachment text indexed: %+v %v", hits, err)
	}
	if hits, err := s.SearchConversations("zebrafile", SearchFilter{}); err != nil || len(hits) != 0 {
		t.Fatalf("attachment text found by SearchConversations: %+v %v", hits, err)
	}
	if hits, err := s.Search("question", 0); err != nil || len(hits) != 1 {
		t.Fatalf("user text not indexed: %+v %v", hits, err)
	}
}
