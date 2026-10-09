// Apply the saved theme before the first paint so there is no flash.
// A plain file (not inline) so the CSP can keep script-src 'self'.
(function () {
  var theme = "system";
  try {
    theme = localStorage.getItem("ffd-theme") || "system";
  } catch (e) {}
  var dark =
    theme === "dark" ||
    (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
})();
