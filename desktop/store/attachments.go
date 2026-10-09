package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrLimit means a conversation already has as many staged attachments as
// the caller allows.
var ErrLimit = errors.New("store: limit reached")

// Attachment is a file attached to a conversation. MsgID is "" while it is
// staged (chosen but not sent yet); sending links it to the user message.
// Text is what the model gets for a text file; an image's bytes are read
// with AttachmentData.
type Attachment struct {
	ID      string
	ConvID  string
	MsgID   string
	Name    string
	Mime    string
	Size    int64
	SHA256  string
	Text    string
	Created time.Time
}

const attachmentCols = `id,conv_id,COALESCE(msg_id,''),name,mime,size,sha256,text,created`

func scanAttachment(r scanner) (Attachment, error) {
	var a Attachment
	var created int64
	if err := r.Scan(&a.ID, &a.ConvID, &a.MsgID, &a.Name, &a.Mime, &a.Size, &a.SHA256, &a.Text, &created); err != nil {
		return Attachment{}, err
	}
	a.Created = ms(created)
	return a, nil
}

// AddAttachment stages a file in a conversation. maxStaged caps the staged
// attachments of the conversation: one more is ErrLimit. An unknown
// conversation is ErrNotFound.
func (s *Store) AddAttachment(a Attachment, data []byte, maxStaged int) (Attachment, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Attachment{}, fmt.Errorf("add attachment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM attachments WHERE conv_id=? AND msg_id IS NULL`, a.ConvID).Scan(&n); err != nil {
		return Attachment{}, fmt.Errorf("add attachment: %w", err)
	}
	if n >= maxStaged {
		return Attachment{}, fmt.Errorf("add attachment: %w", ErrLimit)
	}
	now := nowMS()
	a.ID, a.MsgID, a.Created = newID(), "", ms(now)
	if _, err := tx.Exec(`INSERT INTO attachments(id,conv_id,msg_id,name,mime,size,sha256,text,data,created) VALUES(?,?,NULL,?,?,?,?,?,?,?)`,
		a.ID, a.ConvID, a.Name, a.Mime, a.Size, a.SHA256, a.Text, data, now); err != nil {
		return Attachment{}, fkNotFound("add attachment", err)
	}
	if err := tx.Commit(); err != nil {
		return Attachment{}, fmt.Errorf("add attachment: %w", err)
	}
	return a, nil
}

// StagedAttachments lists the staged attachments of a conversation, oldest
// first.
func (s *Store) StagedAttachments(convID string) ([]Attachment, error) {
	return s.queryAttachments("staged attachments",
		`SELECT `+attachmentCols+` FROM attachments WHERE conv_id=? AND msg_id IS NULL ORDER BY created, rowid`, convID)
}

// MessageAttachments lists the sent attachments of a conversation by
// message id, in the order they were staged.
func (s *Store) MessageAttachments(convID string) (map[string][]Attachment, error) {
	list, err := s.queryAttachments("message attachments",
		`SELECT `+attachmentCols+` FROM attachments WHERE conv_id=? AND msg_id IS NOT NULL ORDER BY created, rowid`, convID)
	if err != nil {
		return nil, err
	}
	out := map[string][]Attachment{}
	for _, a := range list {
		out[a.MsgID] = append(out[a.MsgID], a)
	}
	return out, nil
}

// StagedByID returns the staged attachments named by ids, in that order.
// An id that is unknown, of another conversation or already sent is
// ErrNotFound.
func (s *Store) StagedByID(convID string, ids []string) ([]Attachment, error) {
	out := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		a, err := scanAttachment(s.db.QueryRow(`SELECT `+attachmentCols+` FROM attachments WHERE id=? AND conv_id=? AND msg_id IS NULL`, id, convID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("staged attachment: %w", ErrNotFound)
		}
		if err != nil {
			return nil, fmt.Errorf("staged attachment: %w", err)
		}
		out = append(out, a)
	}
	return out, nil
}

// DeleteStagedAttachment removes a staged attachment of a conversation. A
// sent one, or one of another conversation, is ErrNotFound.
func (s *Store) DeleteStagedAttachment(convID, id string) error {
	return execOne("delete attachment", s.db, `DELETE FROM attachments WHERE id=? AND conv_id=? AND msg_id IS NULL`, id, convID)
}

// AttachmentData returns the stored bytes of an attachment of a
// conversation (images; text files keep none).
func (s *Store) AttachmentData(convID, id string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRow(`SELECT data FROM attachments WHERE id=? AND conv_id=?`, id, convID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("attachment data: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("attachment data: %w", err)
	}
	return data, nil
}

// AppendUserMessage appends a message like AppendMessage and, in the same
// transaction, links the staged attachments ids to it. When one of them is
// no longer staged in the conversation nothing is written and the error is
// ErrNotFound.
func (s *Store) AppendUserMessage(convID, role, partsJSON string, ids []string) (Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, fmt.Errorf("append message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	m, err := appendMessageTx(tx, convID, role, partsJSON)
	if err != nil {
		return Message{}, err
	}
	if len(ids) > 0 {
		args := []any{m.ID, convID}
		for _, id := range ids {
			args = append(args, id)
		}
		res, err := tx.Exec(`UPDATE attachments SET msg_id=? WHERE conv_id=? AND msg_id IS NULL AND id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
		if err != nil {
			return Message{}, fmt.Errorf("append message: %w", err)
		}
		if n, _ := res.RowsAffected(); n != int64(len(ids)) {
			return Message{}, fmt.Errorf("append message: attachment: %w", ErrNotFound)
		}
	}
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("append message: %w", err)
	}
	return m, nil
}

func (s *Store) queryAttachments(op, query string, args ...any) ([]Attachment, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		out = append(out, a)
	}
	return out, rowsErr(rows, op)
}
