package sitesetup

import (
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// Store writes sites to the config file at Path, always through config.Edit
// or config.Overwrite (lock, re-read, atomic 0600 write). Names and URLs are
// stored as given: check them first with NameAndURL, ValidateName or
// NormalizeURL.
type Store struct {
	Path string
	// DropCache, when set, deletes the local cache kept under a site name.
	// It is called after a successful write for each name whose cache no
	// longer belongs to the site now stored under it.
	DropCache func(name string)
}

func (s Store) dropCache(names ...string) {
	if s.DropCache == nil {
		return
	}
	for _, n := range names {
		s.DropCache(n)
	}
}

// Init replaces the config with one holding only this site, as the default.
func (s Store) Init(name string, site config.SiteConfig) error {
	err := config.Overwrite(s.Path, func(f *config.File) error {
		f.Set("default_site", name)
		return f.PutSite(name, site)
	})
	if err == nil {
		s.dropCache(name) // a cache under this name belongs to an earlier site
	}
	return err
}

// Add adds or replaces one site, keeping the rest of the file. A replaced
// site's cache (another login, maybe another server) is dropped.
func (s Store) Add(name string, site config.SiteConfig) error {
	err := config.Edit(s.Path, func(f *config.File) error {
		return f.PutSite(name, site)
	})
	if err == nil {
		s.dropCache(name)
	}
	return err
}

// Rename renames a site, keeping its position and comments; default_site
// follows it.
func (s Store) Rename(oldName, newName string) error {
	if err := config.Edit(s.Path, func(f *config.File) error {
		return f.RenameSite(oldName, newName)
	}); err != nil {
		return err
	}
	// The old name's cache is gone with it; a cache left under the new
	// name by an earlier site of that name is not this site's.
	s.dropCache(oldName, newName)
	return nil
}

// SetURL points a site at another URL. It does not check the credentials
// against it: Verify does.
func (s Store) SetURL(name, siteURL string) error {
	if err := config.Edit(s.Path, func(f *config.File) error {
		return f.SetSiteURL(name, siteURL)
	}); err != nil {
		return err
	}
	s.dropCache(name) // another server: its lists and schemas differ
	return nil
}

// Removed is what Remove changed besides the site itself.
type Removed struct {
	WasDefault bool   // the site was default_site
	NewDefault string // default_site afterwards ("" when no site is left)
}

// Remove deletes a site from the config. An OAuth token is not revoked
// here: RevokeToken does that, before Remove.
func (s Store) Remove(name string) (Removed, error) {
	var r Removed
	if err := config.Edit(s.Path, func(f *config.File) error {
		r.WasDefault = f.Get("default_site") == name
		if err := f.RemoveSite(name); err != nil {
			return err
		}
		r.NewDefault = f.Get("default_site")
		return nil
	}); err != nil {
		return Removed{}, err
	}
	s.dropCache(name)
	return r, nil
}

// SetDefault makes name, which must be a configured site, the default.
// Nothing is written when it already is.
func (s Store) SetDefault(name string) error {
	return config.Edit(s.Path, func(f *config.File) error {
		if !f.HasSite(name) {
			return fmt.Errorf("site %q not found in config (available: %s)", name, strings.Join(f.SiteNames(), ", "))
		}
		if f.Get("default_site") == name {
			return config.ErrUnchanged
		}
		f.Set("default_site", name)
		return nil
	})
}
