package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Profile is a user's profile row. The JSON columns are opaque here; the
// services package defines and validates them. Presets are not stored.
type Profile struct {
	ID            string
	Name          string
	Preset        string // the preset it was duplicated from, or ""
	Mode          string
	ToolsetsJSON  string
	PolicyJSON    string
	DenyToolsJSON string
	CallMethod    bool
	StepLimit     int
	ProviderID    string
	Model         string
	Instructions  string
	KeepHistory   bool
	Created       time.Time
	Updated       time.Time
}

// SiteSettings are the app's own settings for a site, keyed by the site's
// name, with the site's address kept so a renamed site is still found.
type SiteSettings struct {
	Site         string
	URL          string
	Instructions string
	LocalOnly    bool
	Updated      time.Time
}

const profileCols = `id,name,preset,mode,toolsets_json,policy_json,deny_tools_json,call_method,step_limit,provider_id,model,instructions,keep_history,created,updated`

func scanProfile(r scanner) (Profile, error) {
	var p Profile
	var cr, up int64
	if err := r.Scan(&p.ID, &p.Name, &p.Preset, &p.Mode, &p.ToolsetsJSON, &p.PolicyJSON, &p.DenyToolsJSON, &p.CallMethod,
		&p.StepLimit, &p.ProviderID, &p.Model, &p.Instructions, &p.KeepHistory, &cr, &up); err != nil {
		return Profile{}, err
	}
	p.Created, p.Updated = ms(cr), ms(up)
	return p, nil
}

// SaveProfile inserts p (an empty ID gets a new one) or replaces the row with
// p's ID, and returns the stored row.
func (s *Store) SaveProfile(p Profile) (Profile, error) {
	now := nowMS()
	if p.ID == "" {
		p.ID = newID()
	}
	_, err := s.db.Exec(`INSERT INTO profiles(`+profileCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,preset=excluded.preset,mode=excluded.mode,
		toolsets_json=excluded.toolsets_json,policy_json=excluded.policy_json,deny_tools_json=excluded.deny_tools_json,
		call_method=excluded.call_method,step_limit=excluded.step_limit,provider_id=excluded.provider_id,
		model=excluded.model,instructions=excluded.instructions,keep_history=excluded.keep_history,updated=excluded.updated`,
		p.ID, p.Name, p.Preset, p.Mode, p.ToolsetsJSON, p.PolicyJSON, p.DenyToolsJSON, p.CallMethod, p.StepLimit,
		p.ProviderID, p.Model, p.Instructions, p.KeepHistory, now, now)
	if err != nil {
		return Profile{}, fmt.Errorf("save profile: %w", err)
	}
	return s.GetProfile(p.ID)
}

// GetProfile returns ErrNotFound when id is unknown.
func (s *Store) GetProfile(id string) (Profile, error) {
	p, err := scanProfile(s.db.QueryRow(`SELECT `+profileCols+` FROM profiles WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, fmt.Errorf("get profile: %w", ErrNotFound)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get profile: %w", err)
	}
	return p, nil
}

// ListProfiles returns the profiles ordered by name then ID.
func (s *Store) ListProfiles() ([]Profile, error) {
	rows, err := s.db.Query(`SELECT ` + profileCols + ` FROM profiles ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("list profiles: %w", err)
		}
		out = append(out, p)
	}
	if err := rowsErr(rows, "list profiles"); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteProfile removes a profile and, in the same transaction, moves the
// conversations that used it to fallback (whose collected site context is
// dropped, as with SetConversationProfile).
func (s *Store) DeleteProfile(id, fallback string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := execOne("delete profile", tx, `DELETE FROM profiles WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE conversations SET profile_id=?,site_context='' WHERE profile_id=?`, fallback, id); err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	return nil
}

// SetConversationProfile changes a conversation's profile without touching
// the updated time. The collected site context is dropped: what the new
// profile's policy lets the app read may differ.
func (s *Store) SetConversationProfile(id, profileID string) error {
	return execOne("set conversation profile", s.db, `UPDATE conversations SET profile_id=?,site_context='' WHERE id=?`, profileID, id)
}

// SetConversationProvider changes a conversation's provider and model
// without touching the updated time.
func (s *Store) SetConversationProvider(id, providerID, model string) error {
	return execOne("set conversation provider", s.db, `UPDATE conversations SET provider_id=?,model=? WHERE id=?`, providerID, model, id)
}

// SetConversationSiteContext stores the site context collected for a
// conversation.
func (s *Store) SetConversationSiteContext(id, text string) error {
	return execOne("set site context", s.db, `UPDATE conversations SET site_context=? WHERE id=?`, text, id)
}

// SaveSiteSettings inserts or replaces the settings of a site.
func (s *Store) SaveSiteSettings(ss SiteSettings) error {
	_, err := s.db.Exec(`INSERT INTO site_settings(site,url,instructions,local_only,updated) VALUES(?,?,?,?,?)
		ON CONFLICT(site) DO UPDATE SET url=excluded.url,instructions=excluded.instructions,local_only=excluded.local_only,updated=excluded.updated`,
		ss.Site, ss.URL, ss.Instructions, ss.LocalOnly, nowMS())
	if err != nil {
		return fmt.Errorf("save site settings: %w", err)
	}
	return nil
}

// ListSiteSettings returns every site's settings, most recently updated
// first.
func (s *Store) ListSiteSettings() ([]SiteSettings, error) {
	rows, err := s.db.Query(`SELECT site,url,instructions,local_only,updated FROM site_settings ORDER BY updated DESC, site`)
	if err != nil {
		return nil, fmt.Errorf("list site settings: %w", err)
	}
	defer rows.Close()
	var out []SiteSettings
	for rows.Next() {
		var ss SiteSettings
		var up int64
		if err := rows.Scan(&ss.Site, &ss.URL, &ss.Instructions, &ss.LocalOnly, &up); err != nil {
			return nil, fmt.Errorf("list site settings: %w", err)
		}
		ss.Updated = ms(up)
		out = append(out, ss)
	}
	if err := rowsErr(rows, "list site settings"); err != nil {
		return nil, err
	}
	return out, nil
}
