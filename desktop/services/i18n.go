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
	"policyTitle": {
		LangEnglish: "Open access to this site?",
		LangFrench:  "Ouvrir l’accès à ce site ?",
		LangArabic:  "توسيع الوصول إلى هذا الموقع؟",
	},
	// %s is the site name.
	"policyBody": {
		LangEnglish: "These changes let the Assistant and the connected apps do more on %s:",
		LangFrench:  "Ces modifications permettent à l’Assistant et aux apps connectées d’en faire plus sur %s :",
		LangArabic:  "تسمح هذه التغييرات للمساعد وللتطبيقات المرتبطة بفعل المزيد على %s:",
	},
	"policyApply": {
		LangEnglish: "Open access",
		LangFrench:  "Ouvrir l’accès",
		LangArabic:  "توسيع الوصول",
	},
	"policyReadOnlyOff": {
		LangEnglish: "Read only is turned off: changes become possible.",
		LangFrench:  "La lecture seule est désactivée : les modifications deviennent possibles.",
		LangArabic:  "أُوقف وضع القراءة فقط: يصبح التغيير ممكنًا.",
	},
	// The two %s are confirm modes (policyConfirm-*).
	"policyConfirmLooser": {
		LangEnglish: "Asking before risky changes goes from “%s” to “%s”.",
		LangFrench:  "La demande avant les modifications à risque passe de « %s » à « %s ».",
		LangArabic:  "يتغيّر طلب التأكيد قبل التغييرات الحساسة من «%s» إلى «%s».",
	},
	"policyConfirm-always": {
		LangEnglish: "always",
		LangFrench:  "toujours",
		LangArabic:  "دائمًا",
	},
	"policyConfirm-if-supported": {
		LangEnglish: "when the app can ask",
		LangFrench:  "quand l’app peut demander",
		LangArabic:  "عندما يستطيع التطبيق",
	},
	"policyConfirm-never": {
		LangEnglish: "never",
		LangFrench:  "jamais",
		LangArabic:  "أبدًا",
	},
	"policyAllowToolsAdded": {
		LangEnglish: "Tools allowed in addition:",
		LangFrench:  "Outils autorisés en plus :",
		LangArabic:  "أدوات مسموح بها إضافةً:",
	},
	"policyAllowToolsLifted": {
		LangEnglish: "No limit on tools any more. It was limited to:",
		LangFrench:  "Plus de limite sur les outils. La liste était :",
		LangArabic:  "لا حدّ على الأدوات بعد الآن. كانت مقتصرة على:",
	},
	"policyAllowDoctypesAdded": {
		LangEnglish: "Document types allowed in addition:",
		LangFrench:  "Types de document autorisés en plus :",
		LangArabic:  "أنواع مستندات مسموح بها إضافةً:",
	},
	"policyAllowDoctypesLifted": {
		LangEnglish: "No limit on document types any more. It was limited to:",
		LangFrench:  "Plus de limite sur les types de document. La liste était :",
		LangArabic:  "لا حدّ على أنواع المستندات بعد الآن. كانت مقتصرة على:",
	},
	"policyDenyDoctypesRemoved": {
		LangEnglish: "Document types no longer refused:",
		LangFrench:  "Types de document qui ne sont plus refusés :",
		LangArabic:  "أنواع مستندات لم تعد مرفوضة:",
	},
	"policyAllowMethodsAdded": {
		LangEnglish: "Server methods allowed in addition:",
		LangFrench:  "Méthodes serveur autorisées en plus :",
		LangArabic:  "دوال خادم مسموح بها إضافةً:",
	},
	"policyAllowMethodsLifted": {
		LangEnglish: "No limit on server methods any more. It was limited to:",
		LangFrench:  "Plus de limite sur les méthodes serveur. La liste était :",
		LangArabic:  "لا حدّ على دوال الخادم بعد الآن. كانت مقتصرة على:",
	},
	"policyDenyMethodsRemoved": {
		LangEnglish: "Server methods no longer refused:",
		LangFrench:  "Méthodes serveur qui ne sont plus refusées :",
		LangArabic:  "دوال خادم لم تعد مرفوضة:",
	},
	"policySensitiveUnlocked": {
		LangEnglish: "Sensitive document types become writable (users, roles, scripts, settings…):",
		LangFrench:  "Des types de document sensibles deviennent modifiables (utilisateurs, rôles, scripts, paramètres…) :",
		LangArabic:  "تصبح أنواع مستندات حساسة قابلة للتعديل (المستخدمون والأدوار والسكربتات والإعدادات…):",
	},
	"policyDeniedUnlocked": {
		LangEnglish: "Server methods refused by default become callable (running code, keys, apps):",
		LangFrench:  "Des méthodes serveur refusées par défaut deviennent appelables (exécution de code, clés, apps) :",
		LangArabic:  "تصبح دوال خادم مرفوضة افتراضيًا قابلة للاستدعاء (تشغيل شيفرة، مفاتيح، تطبيقات):",
	},
	"policyMethodsEnabled": {
		LangEnglish: "Calling server methods becomes possible again.",
		LangFrench:  "L’appel de méthodes serveur redevient possible.",
		LangArabic:  "يصبح استدعاء دوال الخادم ممكنًا من جديد.",
	},
	"policyWarning": {
		LangEnglish: "If you did not just press Save on the Safety rules screen, do not open access.",
		LangFrench:  "Si vous ne venez pas d’enregistrer dans l’écran Règles de sécurité, n’ouvrez pas l’accès.",
		LangArabic:  "إذا لم تضغط للتو على «حفظ» في شاشة قواعد الأمان، فلا توسّع الوصول.",
	},
	"policyOther": {
		LangEnglish: "Another change that opens access:",
		LangFrench:  "Une autre modification qui ouvre l’accès :",
		LangArabic:  "تغيير آخر يوسّع الوصول:",
	},
	// %d is how many more entries.
	"policyMore": {
		LangEnglish: "and %d more",
		LangFrench:  "et %d de plus",
		LangArabic:  "و%d أخرى",
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
