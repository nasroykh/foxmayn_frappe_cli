// Apply the saved theme and language before the first paint so there is no
// flash of the other theme or direction.
// A plain file (not inline) so the CSP can keep script-src 'self'.
(function () {
  var theme = "system";
  var lang = null;
  try {
    theme = localStorage.getItem("ffd-theme") || "system";
    lang = localStorage.getItem("ffd-language");
  } catch (e) {}
  var dark =
    theme === "dark" ||
    (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);

  // Same rules as detectLanguage in src/i18n/index.ts: the saved language,
  // else the first system language the app speaks, else English.
  var known = function (l) {
    return l === "en" || l === "fr" || l === "ar";
  };
  if (!known(lang)) {
    lang = "en";
    var tags = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language];
    for (var i = 0; i < tags.length; i++) {
      var base = String(tags[i] || "").toLowerCase().split(/[-_]/)[0];
      if (known(base)) {
        lang = base;
        break;
      }
    }
  }
  document.documentElement.lang = lang;
  document.documentElement.dir = lang === "ar" ? "rtl" : "ltr";
})();
