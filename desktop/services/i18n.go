package services

import (
	"strings"
	"sync/atomic"
)

// Languages the app speaks. The page picks one (the saved choice, else the
// system's) and tells Go with AppService.SetLanguage, so the native dialogs
// Go opens speak it too. Errors carry a Key the page translates instead
// (errorKeys); their Message stays English for logs and Detail.
const (
	LangEnglish = "en"
	LangFrench  = "fr"
	LangArabic  = "ar"
)

var uiLanguage atomic.Value // string

func init() { uiLanguage.Store(LangEnglish) }

// normalizeLanguage maps a language tag ("fr-FR", "AR") to a language the
// app speaks; anything else is English.
func normalizeLanguage(tag string) string {
	base := strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(base, "-_"); i >= 0 {
		base = base[:i]
	}
	switch base {
	case LangFrench, LangArabic:
		return base
	}
	return LangEnglish
}

func currentLanguage() string { return uiLanguage.Load().(string) }

// SetLanguage sets the language of the native dialogs ("en", "fr", "ar" or
// a tag such as "fr-FR"; anything else is English) and returns the one in
// use. The page calls it on start and whenever the language changes.
func (s *AppService) SetLanguage(lang string) string {
	l := normalizeLanguage(lang)
	uiLanguage.Store(l)
	return l
}

// dialogStrings holds the strings of the native dialogs, per language. A
// string is a fmt format when its caller passes arguments.
var dialogStrings = map[string]map[string]string{
	"attachTitle": {
		LangEnglish: "Attach files",
		LangFrench:  "Joindre des fichiers",
		LangArabic:  "إرفاق ملفات",
	},
	"attachFilter": {
		LangEnglish: "Text, CSV, JSON, XLSX, DOCX, PDF or images",
		LangFrench:  "Texte, CSV, JSON, XLSX, DOCX, PDF ou images",
		LangArabic:  "نص أو CSV أو JSON أو XLSX أو DOCX أو PDF أو صور",
	},
	"dropTitle": {
		LangEnglish: "Attach dropped files?",
		LangFrench:  "Joindre les fichiers déposés ?",
		LangArabic:  "إرفاق الملفات المسحوبة؟",
	},
	"dropBody": {
		LangEnglish: "These files were dropped on the chat. Attach them?",
		LangFrench:  "Ces fichiers ont été déposés dans la discussion. Les joindre ?",
		LangArabic:  "سُحبت هذه الملفات إلى المحادثة. هل تريد إرفاقها؟",
	},
	// %s is a folder.
	"dropIn": {
		LangEnglish: "in %s",
		LangFrench:  "dans %s",
		LangArabic:  "في %s",
	},
	"dropAttach": {
		LangEnglish: "Attach",
		LangFrench:  "Joindre",
		LangArabic:  "إرفاق",
	},
	"dropCancel": {
		LangEnglish: "Cancel",
		LangFrench:  "Annuler",
		LangArabic:  "إلغاء",
	},
	"exportTitle": {
		LangEnglish: "Export conversation",
		LangFrench:  "Exporter la conversation",
		LangArabic:  "تصدير المحادثة",
	},
	"importTitle": {
		LangEnglish: "Import conversation",
		LangFrench:  "Importer une conversation",
		LangArabic:  "استيراد محادثة",
	},
	"importFilter": {
		LangEnglish: "Conversation (JSON)",
		LangFrench:  "Conversation (JSON)",
		LangArabic:  "محادثة (JSON)",
	},
}

// dialogT is the dialog string key in the current language.
func dialogT(key string) string {
	m := dialogStrings[key]
	if s := m[currentLanguage()]; s != "" {
		return s
	}
	return m[LangEnglish]
}
